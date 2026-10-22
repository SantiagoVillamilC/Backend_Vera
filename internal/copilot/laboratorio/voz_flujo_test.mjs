import {test} from 'node:test';
import assert from 'node:assert/strict';
import {leerVozFlujo, DecodificadorPCM, ReproductorPCM, wavPCM} from './web/voz-flujo.mjs';

const linea = (data) => JSON.stringify({success: true, data, error: null, meta: null}) + '\n';
test('NDJSON soporta bytes partidos y entrega el primer fragmento antes de cerrar', async () => {
  let controlador;
  const r = new Response(new ReadableStream({start(c) { controlador = c; }}));
  const vistos = [], bytes = new TextEncoder().encode(linea({tipo: 'audio', pcm: 'AAABAA==', nota: 'sí'}));
  let primera;
  const recibida = new Promise((r) => { primera = r; });
  const tarea = leerVozFlujo(r, (e) => { vistos.push(e); if (e.tipo === 'audio') primera(); });
  for (const b of bytes) controlador.enqueue(Uint8Array.of(b));
  await recibida;
  assert.equal(vistos.length, 1); assert.equal(vistos[0].nota, 'sí');
  controlador.enqueue(new TextEncoder().encode(linea({tipo: 'fin'}))); controlador.close();
  await tarea; assert.equal(vistos[1].tipo, 'fin');
});
test('rechaza cortes de red y errores aunque ya hubiera audio', async () => {
  await assert.rejects(leerVozFlujo(new Response(linea({tipo: 'audio'})), () => {}), /interrumpió/);
  await assert.rejects(leerVozFlujo(new Response('{"success":false,"error":{"message":"Cuota"}}\n'), () => {}), /Cuota/);
});
test('PCM16 respeta signo y fronteras impares; WAV conserva 24 kHz', () => {
  const d = new DecodificadorPCM();
  assert.equal(d.convertir(Uint8Array.of(0)).length, 0);
  assert.deepEqual([...d.convertir(Uint8Array.of(128, 255, 127))], [-1, 32767 / 32768]); d.terminar();
  const b = wavPCM([Uint8Array.of(0, 128), Uint8Array.of(255, 127)]), v = new DataView(b.buffer);
  assert.equal(v.getUint32(24, true), 24000); assert.equal(v.getUint32(40, true), 4);
  assert.deepEqual([...b.slice(44)], [0, 128, 255, 127]);
  assert.throws(() => wavPCM([Uint8Array.of(0)]), /inválido/);
});
test('agenda audio antes del fin y detener libera los fragmentos pendientes', () => {
  const fuentes = [], inicios = []; let finalizo = 0;
  const contexto = {currentTime: 1, destination: {}, createBuffer(c, n, rate) { return {duration: n / rate, copyToChannel() {}}; }, createBufferSource() { const f = {connect() {}, disconnect() {}, start(t) { inicios.push(t); }, stop() { this.parada = true; }}; fuentes.push(f); return f; }};
  const p = new ReproductorPCM(contexto, () => { finalizo++; });
  p.agregar(new Uint8Array(4800)); p.agregar(new Uint8Array(4800));
  assert.equal(inicios.length, 2); assert.ok(Math.abs(inicios[1] - inicios[0] - 0.1) < 0.000001); assert.equal(finalizo, 0);
  p.finalizar(); fuentes.forEach((f) => f.onended()); assert.equal(finalizo, 1);
  const q = new ReproductorPCM(contexto, () => { finalizo++; }); q.agregar(new Uint8Array(4800)); q.detener();
  assert.equal(fuentes.at(-1).parada, true); assert.equal(q.fuentes.size, 0);
});
