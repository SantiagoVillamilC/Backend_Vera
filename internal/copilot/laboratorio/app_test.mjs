import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';
import {pcmAWav} from './web/audio.mjs';
import {conPlazo, ErrorLaboratorio, errorDelSobre, LimitesSesion} from './web/sesion.mjs';

// Ejecuta los manejadores reales de app.js con navegador/servidor controlados.
// No acredita captura con micrófono físico ni reconocimiento real de Google.
test('una transcripción bloqueada no muestra el texto anterior y se reintenta el mismo clip', async (t) => {
  class Elemento {
    constructor() { this.hidden = false; this.disabled = false; this.textContent = ''; this.value = ''; this.checked = false; this.children = []; this.events = {}; this.options = []; this.classList = {add() {}, remove() {}}; }
    addEventListener(tipo, fn) { this.events[tipo] = fn; }
    append(...hijos) { this.children.push(...hijos); }
    replaceChildren() { this.children = []; }
    removeAttribute(nombre) { delete this[nombre]; }
    querySelectorAll() { return []; }
  }
  const html = await readFile(new URL('./web/index.html', import.meta.url), 'utf8');
  const elementos = new Map([...html.matchAll(/id="([^"]+)"/g)].map((m) => [m[1], new Elemento()]));
  const el = (id) => { assert.ok(elementos.has(id), `elemento ausente: ${id}`); return elementos.get(id); };
  el('voz').value = 'navegador';
  el('voz').options = [{value: 'navegador'}];
  const temporizadores = new Set(), cierre = {};
  const intervalo = (...args) => { const id = setInterval(...args); temporizadores.add(id); return id; };
  t.after(() => { cierre.pagehide?.(); for (const id of temporizadores) clearInterval(id); });
  let permisos = 0, transcripciones = 0, preguntas = 0;
  const cuerpos = [];
  const estado = {modelo: 'prueba', usadas: 1, maximo: 120, turnos: 0, demo: false, voz_natural: false, voz_flujo: false, espera_chat_segundos: 0, espera_voz_segundos: 0};
  class ContextoAudio {
    constructor() { this.state = 'running'; this.sampleRate = 16000; this.audioWorklet = {async addModule() {}}; }
    async resume() {}
    async close() { this.state = 'closed'; }
    createMediaStreamSource() { return {connect() {}}; }
    createGain() { return {gain: {}, connect() {}}; }
  }
  class Worklet {
    constructor() { this.port = {postMessage: () => { this.port.onmessage({data: {pcm: new Float32Array(1600)}}); this.port.onmessage({data: {fin: true}}); }}; }
    connect(destino) { return destino; }
  }
  const context = {
    pcmAWav, conPlazo, ErrorLaboratorio, errorDelSobre, LimitesSesion,
    document: {getElementById: el, createElement: () => new Elemento()},
    window: {AudioWorkletNode: Worklet, addEventListener: (evento, fn) => { cierre[evento] = fn; }},
    navigator: {mediaDevices: {async getUserMedia() { permisos++; return {getTracks: () => [{stop() {}}]}; }}},
    AudioContext: ContextoAudio, AudioWorkletNode: Worklet,
    location: {hash: '#token=ficticio', pathname: '/'}, history: {replaceState() {}},
    sessionStorage: {getItem() {}, setItem() {}},
    URL, URLSearchParams, Blob, AbortController, DOMException, performance,
    setInterval: intervalo, clearInterval, setTimeout, clearTimeout,
    fetch: async (ruta, opciones) => {
      if (ruta === '/api/transcribir') {
        transcripciones++; cuerpos.push(opciones.body);
        if (transcripciones === 1) return new Response(JSON.stringify({success: false, error: {code: 'TranscriptionBlocked', message: 'Google bloqueó esta transcripción.'}}), {status: 422});
        return Response.json({success: true, data: {transcripcion: '¿Puedo pasarle treinta mil a Laura?', estado}});
      }
      if (ruta === '/api/preguntar') { preguntas++; return Response.json({success: true, data: {texto: 'Puedes consultar tus límites.', eventos: [], estado: {...estado, turnos: 1}}}); }
      return Response.json({success: true, data: estado});
    },
  };
  const app = (await readFile(new URL('./web/app.js', import.meta.url), 'utf8')).split('\n').filter((linea) => !linea.startsWith('import ')).join('\n');
  await new vm.Script(`(async () => { ${app} })()`).runInNewContext(context);
  el('transcripcion').hidden = false; el('texto-transcripcion').textContent = 'Pregunta del 11 al 15 de octubre';
  await el('microfono').events.click();
  assert.equal(el('transcripcion').hidden, true); assert.equal(el('texto-transcripcion').textContent, '');
  await el('microfono').events.click();
  assert.match(el('estado').textContent, /No se pudo transcribir/);
  assert.equal(el('reintentar-audio').hidden, false);
  assert.equal(el('texto-transcripcion').textContent, '');
  await el('reintentar-audio').events.click();
  assert.equal(permisos, 1); assert.equal(transcripciones, 2); assert.equal(preguntas, 1);
  assert.equal(cuerpos[0], cuerpos[1]);
  assert.equal(el('reintentar-audio').hidden, true);
  assert.equal(el('texto-transcripcion').textContent, '¿Puedo pasarle treinta mil a Laura?');
  assert.match(el('cuota').textContent, /1 turnos/);
});
