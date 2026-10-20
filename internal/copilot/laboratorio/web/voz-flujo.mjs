// Lee envelopes NDJSON aunque la red divida una línea o un carácter UTF-8.
export async function leerVozFlujo(respuesta, recibir) {
  if (!respuesta.ok) { const e = await respuesta.json(); throw new Error(e.error?.message || 'No se pudo generar la voz.'); }
  if (!respuesta.body) throw new Error('El navegador no permite recibir audio en streaming.');
  const lector = respuesta.body.getReader(), decodificador = new TextDecoder();
  let pendiente = '', fin = false, total = 0;
  const procesar = async (linea) => {
    if (!linea.trim()) return;
    if (fin) throw new Error('Llegaron datos después del final de la voz.');
    const sobre = JSON.parse(linea);
    if (!sobre.success) throw new Error(sobre.error?.message || 'La voz se interrumpió.');
    if (sobre.data.tipo === 'fin') fin = true;
    await recibir(sobre.data);
  };
  try {
    for (;;) {
      const {value, done} = await lector.read();
      if (done) break;
      total += value.byteLength;
      if (total > 8 * 1024 * 1024) throw new Error('El audio superó el límite permitido.');
      pendiente += decodificador.decode(value, {stream: true});
      let corte;
      while ((corte = pendiente.indexOf('\n')) >= 0) { const linea = pendiente.slice(0, corte); pendiente = pendiente.slice(corte + 1); await procesar(linea); }
    }
    pendiente += decodificador.decode();
    if (pendiente.trim()) await procesar(pendiente);
    if (!fin) throw new Error('La voz se interrumpió antes de completarse.');
  } finally { try { await lector.cancel(); } catch { /* La conexión puede estar cerrada. */ } lector.releaseLock(); }
}

// Conserva como máximo un byte al dividir PCM16 entre paquetes.
export class DecodificadorPCM {
  constructor() { this.pendiente = null; }
  convertir(bytes) {
    const entrada = new Uint8Array(bytes.length + (this.pendiente === null ? 0 : 1));
    if (this.pendiente !== null) entrada[0] = this.pendiente;
    entrada.set(bytes, this.pendiente === null ? 0 : 1);
    this.pendiente = entrada.length % 2 ? entrada[entrada.length - 1] : null;
    const vista = new DataView(entrada.buffer), muestras = new Float32Array(Math.floor(entrada.length / 2));
    for (let i = 0; i < muestras.length; i++) muestras[i] = vista.getInt16(i * 2, true) / 32768;
    return muestras;
  }
  terminar() { if (this.pendiente !== null) throw new Error('El audio terminó con una muestra incompleta.'); }
}

export function wavPCM(partes) {
  const tamano = partes.reduce((n, b) => n + b.length, 0);
  if (!tamano || tamano % 2 || tamano > 4 * 1024 * 1024) throw new Error('Audio PCM inválido.');
  const wav = new Uint8Array(44 + tamano), vista = new DataView(wav.buffer);
  const texto = (i, s) => [...s].forEach((c, n) => { wav[i + n] = c.charCodeAt(0); });
  texto(0, 'RIFF'); vista.setUint32(4, wav.length - 8, true); texto(8, 'WAVEfmt ');
  vista.setUint32(16, 16, true); vista.setUint16(20, 1, true); vista.setUint16(22, 1, true);
  vista.setUint32(24, 24000, true); vista.setUint32(28, 48000, true); vista.setUint16(32, 2, true); vista.setUint16(34, 16, true);
  texto(36, 'data'); vista.setUint32(40, tamano, true);
  let posicion = 44; for (const b of partes) { wav.set(b, posicion); posicion += b.length; }
  return wav;
}

// Agenda fragmentos consecutivos; no espera al WAV completo para reproducir.
export class ReproductorPCM {
  constructor(contexto, alTerminar) {
    this.contexto = contexto; this.alTerminar = alTerminar; this.decodificador = new DecodificadorPCM();
    this.fuentes = new Set(); this.siguiente = 0; this.terminado = false; this.detenido = false;
  }
  agregar(bytes) {
    if (this.detenido) return;
    const muestras = this.decodificador.convertir(bytes); if (!muestras.length) return;
    const buffer = this.contexto.createBuffer(1, muestras.length, 24000); buffer.copyToChannel(muestras, 0);
    const fuente = this.contexto.createBufferSource(); fuente.buffer = buffer; fuente.connect(this.contexto.destination);
    this.fuentes.add(fuente);
    fuente.onended = () => { this.fuentes.delete(fuente); fuente.disconnect(); this.comprobarFin(); };
    this.siguiente = Math.max(this.contexto.currentTime + 0.06, this.siguiente);
    fuente.start(this.siguiente); this.siguiente += buffer.duration;
  }
  finalizar() { this.decodificador.terminar(); this.terminado = true; this.comprobarFin(); }
  comprobarFin() { if (!this.detenido && this.terminado && !this.fuentes.size) { this.detenido = true; this.alTerminar(); } }
  detener() { this.detenido = true; for (const f of this.fuentes) { f.onended = null; f.stop(); f.disconnect(); } this.fuentes.clear(); }
}
