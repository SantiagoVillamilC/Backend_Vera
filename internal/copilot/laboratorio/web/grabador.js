class GrabadorVera extends AudioWorkletProcessor {
  constructor() {
    super(); this.bloque = new Float32Array(4096); this.pos = 0; this.activo = true;
    this.port.onmessage = (e) => { if (e.data === 'fin') { this.enviar(); this.activo = false; this.port.postMessage({fin: true}); } };
  }
  enviar() { if (this.pos) { const b = this.bloque.slice(0, this.pos); this.port.postMessage({pcm: b}, [b.buffer]); this.pos = 0; } }
  process(entradas) {
    if (!this.activo) return false;
    const canales = entradas[0];
    if (canales?.length) for (let i = 0; i < canales[0].length; i++) {
      let valor = 0; for (const canal of canales) valor += canal[i];
      this.bloque[this.pos++] = valor / canales.length;
      if (this.pos === this.bloque.length) this.enviar();
    }
    return true;
  }
}
registerProcessor('grabador-vera', GrabadorVera);
