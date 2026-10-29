import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { encode } from "silk-wasm";

const script = fileURLToPath(new URL("./decode-silk.mjs", import.meta.url));
function run(input) {
  return spawnSync(process.execPath, ["--max-old-space-size=128", script], {
    input, env: {}, timeout: 15000, maxBuffer: 16 * 1024 * 1024,
  });
}

test("decodes an actual SILK recording to a bounded mono WAV", async () => {
  const pcm = Buffer.alloc(24000 * 2 / 5);
  for (let i = 0; i < pcm.length / 2; i++) pcm.writeInt16LE(Math.round(1000 * Math.sin(i / 10)), i * 2);
  const encoded = await encode(pcm, 24000);
  const result = run(encoded.data);
  assert.equal(result.status, 0, result.stderr.toString());
  assert.equal(result.stdout.toString("ascii", 0, 4), "RIFF");
  assert.equal(result.stdout.toString("ascii", 8, 12), "WAVE");
  assert.equal(result.stdout.readUInt32LE(24), 24000);
  assert.equal(result.stdout.readUInt32LE(40), result.stdout.length - 44);
  assert.ok(result.stdout.length > 44);
});

test("rejects invalid input without echoing media", () => {
  const result = run(Buffer.from("private invalid media"));
  assert.equal(result.status, 1);
  assert.equal(result.stdout.length, 0);
  assert.equal(result.stderr.toString(), "audio decoding failed\n");
});
