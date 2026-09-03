#!/usr/bin/env python3
"""A WebSocket client for scripts/smoke.sh, standard library only (P6d,
decisions.md mocker-p6d-websocket D12).

Why this exists: the smoke suite speaks curl and jq, curl's CLI can only
RECEIVE WebSocket frames, and the runtime image is distroless — so the one
WebSocket client the acceptance needs runs on the host, in python3 (>= 3.10),
with nothing to install. It does the handshake, sends masked text frames in
argv order, prints every received data frame with a millisecond offset, and
prints the close code last.

Output, one line each, in order:
  status 101                       the upgrade succeeded
  status <code> <body>             the upgrade was refused (the HTTP answer)
  <ms> text <payload>              a text frame, payload verbatim
  <ms> binary <hex>                a binary frame, payload as hex
  close <code> <reason>            the peer closed (code from its frame)
  close timeout                    --timeout elapsed; the client closed 1000
  close idle                       --idle-ms passed with the burst sent and
                                   nothing arriving; the client closed 1000
Exit status 0 on a clean handshake and close; 2 on a refused handshake; 1 on
a transport error.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import os
import socket
import ssl
import struct
import sys
import threading
import time
from urllib.parse import urlparse

GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--url", required=True, help="ws://host:port/path")
    p.add_argument("--host", help="Host header (the mock plane routes by it)")
    p.add_argument("--origin", help="Origin header; omitted = no header")
    p.add_argument("--cacert", help="wss:// only: PEM root to verify the server certificate against (the smoke passes Caddy's local root)")
    p.add_argument("--connect", help="dial this address instead of the URL's host, keeping the URL's host for SNI and the Host header (curl --resolve's job)")
    p.add_argument("--send", action="append", default=[], help="a text frame to send after the handshake (repeatable, in order)")
    p.add_argument("--burst", help="N:SIZE — send N text frames of SIZE bytes ('x' * SIZE) after --send, from a second thread, so reading (after --read-after-ms) overlaps the sending")
    p.add_argument("--read-after-ms", type=int, default=0, help="do not read for this long after the sends started")
    p.add_argument("--idle-ms", type=int, default=0, help="once the burst is fully sent, close 1000 after this long without a data frame (0 = read until --timeout)")
    p.add_argument("--expect", type=int, default=0, help="stop after this many data frames and close 1000 (0 = until close/timeout)")
    p.add_argument("--timeout", type=float, default=5.0, help="seconds to keep reading before closing 1000")
    return p.parse_args()


# Every write to the socket goes through send(): the burst thread and the
# reader (its pongs, the close) would otherwise interleave the BYTES of two
# frames, and a socket timeout the reader lowers mid-burst must not abort a
# sendall in the other thread — send() retries on a timeout with what is
# left, so the burst survives any timeout the reader picks.
_send_lock = threading.Lock()


def send(sock: socket.socket, data: bytes) -> None:
    view = memoryview(data)
    with _send_lock:
        while view:
            try:
                n = sock.send(view)
            except TimeoutError:
                continue
            view = view[n:]


def frame(opcode: int, payload: bytes) -> bytes:
    """One masked client frame (RFC 6455 §5.2; clients MUST mask)."""
    head = bytes([0x80 | opcode])
    n = len(payload)
    if n < 126:
        head += bytes([0x80 | n])
    elif n < 65536:
        head += bytes([0x80 | 126]) + struct.pack(">H", n)
    else:
        head += bytes([0x80 | 127]) + struct.pack(">Q", n)
    # Masking is mandatory client-to-server with an unpredictable key
    # (RFC 6455 §5.3). The XOR is done on two big integers rather than
    # byte by byte: a Python-level loop over a 6 MB burst (A3(f)) took long
    # enough to hit the server's lifetime before the client ever read.
    mask = os.urandom(4)
    if n == 0:
        return head + mask
    keystream = (mask * (n // 4 + 1))[:n]
    masked = (int.from_bytes(payload, "big") ^ int.from_bytes(keystream, "big")).to_bytes(n, "big")
    return head + mask + masked


class Reader:
    def __init__(self, sock: socket.socket) -> None:
        self.sock = sock
        self.buf = b""

    def exactly(self, n: int) -> bytes:
        while len(self.buf) < n:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise ConnectionError("eof")
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def frame(self) -> tuple[int, bytes]:
        """Returns (opcode, payload) of one complete message, unmasked server
        frames only; fragments are joined; control frames are handled here
        (ping answered, close returned as opcode 8)."""
        message = b""
        first_op = None
        while True:
            b0, b1 = self.exactly(2)
            fin, op = b0 & 0x80, b0 & 0x0F
            n = b1 & 0x7F
            if n == 126:
                n = struct.unpack(">H", self.exactly(2))[0]
            elif n == 127:
                n = struct.unpack(">Q", self.exactly(8))[0]
            payload = self.exactly(n)
            if op == 0x9:  # ping -> pong
                send(self.sock, frame(0xA, payload))
                continue
            if op == 0xA:  # pong
                continue
            if op == 0x8:
                return 0x8, payload
            if op != 0:
                first_op = op
            message += payload
            if fin:
                return first_op or 0x1, message


def main() -> int:
    a = parse_args()
    u = urlparse(a.url)
    port = u.port or (443 if u.scheme == "wss" else 80)
    path = u.path or "/"
    if u.query:
        path += "?" + u.query
    host_header = a.host or u.netloc
    key = base64.b64encode(os.urandom(16)).decode()
    req = [
        f"GET {path} HTTP/1.1",
        f"Host: {host_header}",
        "Upgrade: websocket",
        "Connection: Upgrade",
        f"Sec-WebSocket-Key: {key}",
        "Sec-WebSocket-Version: 13",
    ]
    if a.origin:
        req.append(f"Origin: {a.origin}")
    raw = ("\r\n".join(req) + "\r\n\r\n").encode()

    sock = socket.create_connection((a.connect or u.hostname, port), timeout=a.timeout + 5)
    if u.scheme == "wss":
        # A real client's TLS: verify the chain against --cacert (or the
        # system store when none is given) and send SNI for the URL's host,
        # which is what makes Caddy pick the wildcard block. Everything
        # after the handshake is the same framing over the wrapped socket.
        ctx = ssl.create_default_context(cafile=a.cacert)
        sock = ctx.wrap_socket(sock, server_hostname=u.hostname)
    sock.sendall(raw)
    rd = Reader(sock)
    # The handshake response: headers up to the blank line.
    head = b""
    while b"\r\n\r\n" not in head:
        chunk = sock.recv(4096)
        if not chunk:
            print("status 0 eof")
            return 1
        head += chunk
    head, rest = head.split(b"\r\n\r\n", 1)
    rd.buf = rest
    lines = head.decode(errors="replace").split("\r\n")
    status = int(lines[0].split(" ")[1])
    if status != 101:
        headers = {}
        for ln in lines[1:]:
            if ":" in ln:
                k, v = ln.split(":", 1)
                headers[k.strip().lower()] = v.strip()
        body = rd.buf
        want = int(headers.get("content-length", "0") or 0)
        while len(body) < want:
            chunk = sock.recv(65536)
            if not chunk:
                break
            body += chunk
        print(f"status {status} {body.decode(errors='replace').strip()}")
        return 2
    accept = base64.b64encode(hashlib.sha1((key + GUID).encode()).digest()).decode()
    if not any(ln.lower().startswith("sec-websocket-accept:") and ln.split(":", 1)[1].strip() == accept for ln in lines):
        print("status 101 bad-accept")
        return 1
    print("status 101", flush=True)

    t0 = time.monotonic()
    for s in a.send:
        send(sock, frame(0x1, s.encode()))

    # The burst runs on its own thread so the reader below can start while
    # frames are still going out. The first shape — send everything, THEN
    # sleep, THEN read — deadlocked on a slow CI runner: the server's echo
    # write blocked on a client that was not reading, its per-frame deadline
    # (MOCKER_STREAM_FRAME_TIMEOUT, 4 s in the smoke) expired before the
    # 6 MB burst had left the client, and the connection closed 1001 with
    # zero echoes delivered. With the sends overlapping the reads the write
    # side is never held past --read-after-ms, whatever the box's speed.
    burst_done = threading.Event()
    burst_error: list[str] = []

    def run_burst() -> None:
        try:
            n, size = (int(x) for x in a.burst.split(":", 1))
            payload = b"x" * size
            for _ in range(n):
                send(sock, frame(0x1, payload))
        except (OSError, ConnectionError) as e:
            burst_error.append(str(e))
        finally:
            burst_done.set()

    if a.burst:
        threading.Thread(target=run_burst, name="burst", daemon=True).start()
    else:
        burst_done.set()
    if a.read_after_ms:
        time.sleep(a.read_after_ms / 1000)

    received = 0
    deadline = time.monotonic() + a.timeout
    last_frame = time.monotonic()
    while True:
        remaining = deadline - time.monotonic()
        idle = a.idle_ms and burst_done.is_set() and (time.monotonic() - last_frame) * 1000 >= a.idle_ms
        if remaining <= 0 or idle or (a.expect and received >= a.expect):
            try:
                send(sock, frame(0x8, struct.pack(">H", 1000)))
                sock.settimeout(2)
                while True:
                    op, payload = rd.frame()
                    if op == 0x8:
                        break
            except (OSError, ConnectionError):
                pass
            if burst_error:
                print(f"burst error {burst_error[0]}", flush=True)
            if a.expect and received >= a.expect:
                print("close 1000 expected", flush=True)
            elif idle:
                print("close idle", flush=True)
            else:
                print("close timeout", flush=True)
            return 0
        wait = remaining
        if a.idle_ms:
            wait = min(wait, max(a.idle_ms / 1000, 0.05))
        sock.settimeout(wait)
        try:
            op, payload = rd.frame()
        except TimeoutError:
            continue
        except (OSError, ConnectionError):
            print("close 1006 abnormal", flush=True)
            return 1
        ms = int((time.monotonic() - t0) * 1000)
        if op == 0x8:
            code = struct.unpack(">H", payload[:2])[0] if len(payload) >= 2 else 1005
            reason = payload[2:].decode(errors="replace")
            try:
                send(sock, frame(0x8, payload[:2]))
            except OSError:
                pass
            print(f"close {code} {reason}", flush=True)
            return 0
        received += 1
        last_frame = time.monotonic()
        if op == 0x2:
            print(f"{ms} binary {payload.hex()}", flush=True)
        else:
            print(f"{ms} text {payload.decode(errors='replace')}", flush=True)


if __name__ == "__main__":
    sys.exit(main())
