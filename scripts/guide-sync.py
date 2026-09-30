#!/usr/bin/env python3
"""Generate content-addressed guides; --check validates without writes.

Canonical hash input is UTF-8 sorted-key compact JSON (ensure_ascii=False)
containing declarations plus SHA-256 of each source's bytes with generated
frontmatter guideSetId/manifestHash removed, and each served markdown body.
Generated identities are excluded to avoid self-reference. ManifestHash and
GuideSetID use the same SHA-256 digest. Changing source text or declarations
changes the identity; a running binary serves only its embedded immutable set.
"""

import hashlib
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
SKILL = ROOT / "skills/mocker"
EMBEDDED = ROOT / "internal/guide"


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def canonical(data):
    return json.dumps(
        data, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode()


def without_identity(text):
    if not text.startswith("---\n"):
        return text
    front, separator, body = text[4:].partition("\n---\n")
    front = "\n".join(
        line
        for line in front.splitlines()
        if not line.startswith(("  guideSetId:", "  manifestHash:"))
    )
    return "---\n" + front + separator + body


def body(text):
    if text.startswith("---\n"):
        _, separator, rest = text[4:].partition("\n---\n")
        if separator:
            return rest.lstrip("\n")
    return text


def validate_metadata(text, workflow):
    """Check entrypoint metadata using the deliberately simple frontmatter schema."""
    front = text[4:].partition("\n---\n")[0]
    metadata = False
    fields = {}
    for line in front.splitlines():
        if line == "metadata:":
            metadata = True
            continue
        if metadata and line.startswith("  ") and ":" in line:
            key, value = line.strip().split(":", 1)
            try:
                fields[key] = json.loads(value.strip())
            except json.JSONDecodeError:
                raise SystemExit(
                    f"entrypoint {workflow['entrypoint']}: metadata must use JSON string scalars"
                ) from None
        elif line and not line.startswith(" "):
            metadata = False
    for name in ("workflowId", "workflowVersion"):
        if fields.get(name) != workflow[name]:
            raise SystemExit(f"entrypoint {workflow['entrypoint']}: incorrect {name}")
    for name in ("requiredModelSchemaVersions", "requiredCapabilities"):
        try:
            items = json.loads(fields.get(name, ""))
        except (json.JSONDecodeError, TypeError):
            raise SystemExit(
                f"entrypoint {workflow['entrypoint']}: {name} must be a JSON-list string"
            ) from None
        if items != workflow[name]:
            raise SystemExit(f"entrypoint {workflow['entrypoint']}: incorrect {name}")


def generate(check):
    declarations = json.loads((SKILL / "guide-sources.json").read_text())
    sources = []
    texts = {}
    for source in declarations["sources"]:
        text = without_identity((SKILL / source["source"]).read_text())
        texts[source["topic"]] = text
        sources.append(
            dict(
                source,
                sourceHash=digest(text.encode()),
                contentHash=digest(body(text).encode()),
            )
        )
    base = {"sources": sources, "workflows": declarations["workflows"]}
    manifest_hash = digest(canonical(base))
    set_id = manifest_hash
    workflows = []
    outputs = {}
    for workflow in base["workflows"]:
        topics = [
            {"topic": s["topic"], "contentHash": s["contentHash"]}
            for s in sources
            if s["workflowId"] == workflow["workflowId"]
        ]
        workflows.append(
            dict(workflow, guideSetId=set_id, manifestHash=manifest_hash, topics=topics)
        )
    for source in sources:
        text = texts[source["topic"]]
        if text.startswith("---\n"):
            text = text.replace(
                "\n---\n",
                f'\n  guideSetId: "{set_id}"\n  manifestHash: "{manifest_hash}"\n---\n',
                1,
            )
            outputs[SKILL / source["source"]] = text
        outputs[EMBEDDED / source["embedded"]] = text
    for workflow in base["workflows"]:
        validate_metadata(
            outputs[
                EMBEDDED
                / next(
                    s["embedded"]
                    for s in sources
                    if s["topic"] == workflow["entrypoint"]
                )
            ],
            workflow,
        )
    manifest = dict(
        base, guideSetId=set_id, manifestHash=manifest_hash, workflows=workflows
    )
    manifest_text = json.dumps(manifest, indent=2, ensure_ascii=False) + "\n"
    outputs[SKILL / "guide-manifest.json"] = manifest_text
    outputs[EMBEDDED / "manifest.json"] = manifest_text
    stale = []
    for path, text in outputs.items():
        if check:
            if not path.exists() or path.read_text() != text:
                stale.append(str(path.relative_to(ROOT)))
        else:
            path.write_text(text)
    if stale:
        raise SystemExit("guide-sync drift: " + ", ".join(stale))
    print(set_id)


if __name__ == "__main__":
    generate("--check" in sys.argv)
