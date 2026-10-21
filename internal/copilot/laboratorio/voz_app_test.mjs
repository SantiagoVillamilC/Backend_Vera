import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';
import {conPlazo, ErrorLaboratorio, errorDelSobre, LimitesSesion} from './web/sesion.mjs';
import {leerVozFlujo, wavPCM, ReproductorPCM} from './web/voz-flujo.mjs';

async function laboratorio(t, {local = true, cartesia = false, audioBloqueado = false, vozFalla = false} = {}) {
  class Elemento {
    constructor() { Object.assign(this, {hidden: false, disabled: false, textContent: '', value: '', checked: false, children: [], events: {}, options: [], classList: {add() {}, remove() {}}}); }
    addEventListener(tipo, fn) { this.events[tipo] = fn; }
    append(...hijos) { this.children.push(...hijos); }
    add(opcion) { this.options.push(opcion); opcion.remove = () => { this.options = this.options.filter((v) => v !== opcion); }; }
    replaceChildren() { this.children = []; }
    querySelectorAll() { return []; }
  }
  const html = await readFile(new URL('./web/index.html', import.meta.url), 'utf8');
  const elementos = new Map([...html.matchAll(/id="([^"]+)"/g)].map((m) => [m[1], new Elemento()]));
  const el = (id) => { assert.ok(elementos.has(id), id); return elementos.get(id); };
  for (const value of ['cartesia:Catalina', 'gemini:Aoede', 'local:Dora', 'navegador']) el('voz').add({value});
  el('voz').value = 'cartesia:Catalina'; el('automatica').checked = true; el('respaldo').checked = true;
  let navegador = 0, reproducciones = 0;
  const solicitudes = [], cierres = {}, relojes = new Set(), eventosVoces = {};
  const estado = {modelo: 'prueba', usadas: 1, maximo: 120, turnos: 1, voz_natural: true, voz_local: local, voz_cartesia: cartesia, caracteres_cartesia: 20, max_caracteres_cartesia: 5000, espera_cartesia_segundos: 0, voz_flujo: true, espera_voz_segundos: 3600, espera_chat_segundos: 0};
  class Contexto {
    constructor() { this.state = 'running'; this.currentTime = 0; this.destination = {}; }
    async resume() {} async close() {}
    createBuffer(c, n, rate) { return {duration: n / rate, copyToChannel() {}}; }
    createBufferSource() { return {connect() {}, disconnect() {}, start() { reproducciones++; }, stop() {}}; }
  }
  const speechSynthesis = {getVoices: () => [{name: 'Sistema', lang: 'es-ES', voiceURI: 'es-sistema'}], cancel() {}, speak() { navegador++; }, addEventListener(evento, fn) { eventosVoces[evento] = fn; }};
  const contexto = {
    conPlazo, ErrorLaboratorio, errorDelSobre, LimitesSesion, leerVozFlujo, wavPCM, ReproductorPCM,
    document: {getElementById: el, createElement: () => new Elemento()},
    window: {AudioContext: Contexto, speechSynthesis, addEventListener: (e, fn) => { cierres[e] = fn; }},
    AudioContext: Contexto, speechSynthesis, SpeechSynthesisUtterance: class {}, Option: class {constructor(nombre, value) { this.value = value; }},
    Audio: class { async play() { if (audioBloqueado) throw new DOMException('Autoplay', 'NotAllowedError'); reproducciones++; } pause() {} },
    navigator: {}, location: {hash: '#token=ficticio', pathname: '/'}, history: {replaceState() {}}, sessionStorage: {getItem() {}, setItem() {}},
    URL, URLSearchParams, Blob, Uint8Array, AbortController, DOMException, performance, atob,
    setInterval: (...args) => { const reloj = setInterval(...args); relojes.add(reloj); return reloj; }, clearInterval, setTimeout, clearTimeout,
    fetch: async (ruta, opciones = {}) => {
      solicitudes.push({ruta, body: opciones.body});
      if (ruta === '/api/preguntar') return Response.json({success: true, data: {texto: 'Hola, soy Vera.', eventos: [], estado}});
      if (ruta === '/api/voz/flujo') {
        if (vozFalla) return Response.json({success: false, error: {code: 'Unavailable', message: 'Voz local indisponible.'}}, {status: 503});
        return new Response([
          {tipo: 'wav', audio: Buffer.from(wavPCM([Uint8Array.of(0, 0, 1, 0)])).toString('base64')},
          {tipo: 'fin', fuente: cartesia ? 'cartesia' : 'kokoro', motivo_respaldo: cartesia ? '' : 'GeminiQuota', estado},
        ].map((data) => JSON.stringify({success: true, data}) + '\n').join(''));
      }
      return Response.json({success: true, data: estado});
    },
  };
  t.after(() => { cierres.pagehide?.(); for (const id of relojes) clearInterval(id); });
  const app = (await readFile(new URL('./web/app.js', import.meta.url), 'utf8')).split('\n').filter((l) => !l.startsWith('import ')).join('\n');
  await new vm.Script(`(async () => { ${app} })()`).runInNewContext(contexto);
  return {el, solicitudes, eventosVoces, navegador: () => navegador, reproducciones: () => reproducciones};
}

test('cuota Gemini en pausa sigue solicitando respaldo neural y conserva Dora al cargar voces del sistema', async (t) => {
  const lab = await laboratorio(t);
  await lab.el('ejemplo').events.click();
  assert.equal(lab.navegador(), 0); assert.equal(lab.reproducciones(), 1);
  assert.match(lab.el('resultado-voz').textContent, /Dora · neural local/);
  assert.equal(lab.solicitudes.filter((s) => s.ruta === '/api/voz/flujo').length, 1);
  await lab.el('escuchar').events.click();
  assert.equal(lab.reproducciones(), 2);
  assert.equal(lab.solicitudes.filter((s) => s.ruta === '/api/voz/flujo').length, 1);
  lab.el('voz').value = 'local:Dora'; lab.eventosVoces.voiceschanged();
  assert.equal(lab.el('voz').value, 'local:Dora'); assert.ok(lab.el('voz').options.some((v) => v.value === 'local:Dora'));
});

test('autoplay bloqueado pide un clic y nunca cambia a voz robótica', async (t) => {
  const lab = await laboratorio(t, {audioBloqueado: true});
  await lab.el('ejemplo').events.click();
  assert.equal(lab.navegador(), 0); assert.match(lab.el('error').textContent, /requiere un clic/);
});

test('voz del navegador sólo aparece si también falla el respaldo neural', async (t) => {
  const lab = await laboratorio(t, {vozFalla: true});
  await lab.el('ejemplo').events.click();
  assert.equal(lab.navegador(), 1); assert.match(lab.el('resultado-voz').textContent, /respaldo final/);
});

test('Catalina conserva selección, audio y cuota independiente de Gemini', async (t) => {
  const lab = await laboratorio(t, {cartesia: true, local: false});
  assert.equal(lab.el('voz').value, 'cartesia:Catalina');
  await lab.el('ejemplo').events.click();
  assert.equal(lab.navegador(), 0); assert.equal(lab.reproducciones(), 1);
  assert.match(lab.el('resultado-voz').textContent, /Catalina · Cartesia/);
  assert.equal(lab.solicitudes.find((s) => s.ruta === '/api/voz/flujo').body, 'Catalina');
  await lab.el('escuchar').events.click();
  assert.equal(lab.solicitudes.filter((s) => s.ruta === '/api/voz/flujo').length, 1);
  lab.eventosVoces.voiceschanged();
  assert.equal(lab.el('voz').value, 'cartesia:Catalina');
  assert.ok(lab.el('voz').options.some((v) => v.value === 'cartesia:Catalina'));
});
