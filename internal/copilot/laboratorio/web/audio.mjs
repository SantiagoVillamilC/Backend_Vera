// PCM del micrófono -> WAV mono de 16 kHz y 16 bits, sin dependencias.
export function pcmAWav(bloques, frecuencia) {
  if (!Number.isFinite(frecuencia) || frecuencia < 8000 || frecuencia > 192000) throw new Error('Frecuencia de audio no válida.');
  const longitud = Math.min(bloques.reduce((n, b) => n + b.length, 0), Math.floor(frecuencia * 20));
  if (!longitud) throw new Error('La grabación está vacía.');
  const pcm = new Float32Array(longitud);
  let pos = 0;
  for (const bloque of bloques) { const n = Math.min(bloque.length, longitud - pos); if (!n) break; pcm.set(bloque.subarray(0, n), pos); pos += n; }
  const muestras = Math.floor(longitud * 16000 / frecuencia);
  const salida = new ArrayBuffer(44 + muestras * 2), vista = new DataView(salida);
  const texto = (p, s) => { for (let i = 0; i < s.length; i++) vista.setUint8(p + i, s.charCodeAt(i)); };
  texto(0, 'RIFF'); vista.setUint32(4, salida.byteLength - 8, true); texto(8, 'WAVE'); texto(12, 'fmt ');
  vista.setUint32(16, 16, true); vista.setUint16(20, 1, true); vista.setUint16(22, 1, true);
  vista.setUint32(24, 16000, true); vista.setUint32(28, 32000, true); vista.setUint16(32, 2, true); vista.setUint16(34, 16, true);
  texto(36, 'data'); vista.setUint32(40, muestras * 2, true);
  for (let i = 0; i < muestras; i++) {
    const p = i * frecuencia / 16000, a = Math.floor(p), f = p - a;
    const valor = Math.max(-1, Math.min(1, pcm[a] * (1 - f) + pcm[Math.min(a + 1, longitud - 1)] * f));
    vista.setInt16(44 + i * 2, Math.round(valor * (valor < 0 ? 32768 : 32767)), true);
  }
  return salida;
}
