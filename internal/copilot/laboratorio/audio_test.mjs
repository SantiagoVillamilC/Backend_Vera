import test from 'node:test';
import assert from 'node:assert/strict';
import {pcmAWav} from './web/audio.mjs';

test('WAV mono de 16 kHz con PCM de 48 kHz', () => {
  const pcm = new Float32Array(48000).fill(0.5), b = pcmAWav([pcm], 48000), d = new DataView(b);
  assert.equal(new TextDecoder().decode(b.slice(0, 4)), 'RIFF');
  assert.equal(d.getUint32(4, true), b.byteLength - 8);
  assert.equal(d.getUint16(22, true), 1);
  assert.equal(d.getUint32(24, true), 16000);
  assert.equal(d.getUint32(40, true), 32000);
  assert.equal(d.getInt16(44, true), 16384);
});
test('limita a 20 segundos y evita desbordamiento de muestras', () => {
  const b = pcmAWav([new Float32Array(44100 * 22).fill(5)], 44100), d = new DataView(b);
  assert.equal(b.byteLength, 640044);
  assert.equal(d.getInt16(44, true), 32767);
});
test('rechaza audio vacío y frecuencia inválida', () => {
  assert.throws(() => pcmAWav([], 48000));
  assert.throws(() => pcmAWav([new Float32Array(20)], NaN));
});
