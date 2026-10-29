import { decode, getDuration, isSilk } from "silk-wasm";

const limit = 16 * 1024 * 1024;
const sampleRate = 24000;

try {
  const chunks = [];
  let size = 0;
  for await (const chunk of process.stdin) {
    size += chunk.length;
    if (size > limit) throw new Error("input limit");
    chunks.push(chunk);
  }
  const input = Buffer.concat(chunks);
  if (!isSilk(input)) throw new Error("invalid SILK");
  const duration = getDuration(input);
  if (!Number.isFinite(duration) || duration <= 0 || duration > 120000) {
    throw new Error("duration limit");
  }
  const decoded = await decode(input, sampleRate);
  const pcm = Buffer.from(decoded.data);
  if (!pcm.length || pcm.length % 2 || pcm.length > sampleRate * 2 * 120) {
    throw new Error("output limit");
  }
  const header = Buffer.alloc(44);
  header.write("RIFF");
  header.writeUInt32LE(36 + pcm.length, 4);
  header.write("WAVEfmt ", 8);
  header.writeUInt32LE(16, 16);
  header.writeUInt16LE(1, 20);
  header.writeUInt16LE(1, 22);
  header.writeUInt32LE(sampleRate, 24);
  header.writeUInt32LE(sampleRate * 2, 28);
  header.writeUInt16LE(2, 32);
  header.writeUInt16LE(16, 34);
  header.write("data", 36);
  header.writeUInt32LE(pcm.length, 40);
  process.stdout.write(Buffer.concat([header, pcm]));
} catch {
  process.stderr.write("audio decoding failed\n");
  process.exitCode = 1;
}
