// @vitest-environment node
import { expect, it, vi } from "vitest";
import { previewLayoutCommands } from "./previewLayout";

it("chains 201 moves through three preview documents without publishing intermediate changes", async () => {
  const preview = vi.fn(async (document: string, commands: number[]) => ({
    document: `${document}:${commands.length}`,
  }));
  const commands = Array.from({ length: 201 }, (_, index) => index);
  expect(await previewLayoutCommands("original", commands, preview)).toEqual({
    document: "original:100:100:1",
  });
  expect(preview.mock.calls.map(([document, commands]) => [document, commands.length])).toEqual([
    ["original", 100],
    ["original:100", 100],
    ["original:100:100", 1],
  ]);
  expect(commands).toHaveLength(201);
});

it("does not request another batch after cancellation during a preview", async () => {
  const controller = new AbortController();
  const preview = vi.fn(async () => {
    controller.abort();
    return { document: "intermediate" };
  });
  await expect(
    previewLayoutCommands("original", Array(101).fill(0), preview, controller.signal),
  ).rejects.toMatchObject({ name: "AbortError" });
  expect(preview).toHaveBeenCalledTimes(1);
});

it("does not return a partially arranged document when a later batch fails", async () => {
  const preview = vi
    .fn()
    .mockResolvedValueOnce({ document: "partial" })
    .mockRejectedValueOnce(new Error("rejected"));
  await expect(previewLayoutCommands("original", Array(101).fill(0), preview)).rejects.toThrow(
    "rejected",
  );
});
