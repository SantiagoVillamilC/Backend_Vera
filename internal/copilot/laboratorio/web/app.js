import {pcmAWav} from '/audio.mjs';
import {leerVozFlujo, wavPCM, ReproductorPCM} from '/voz-flujo.mjs';
import {ErrorLaboratorio, errorDelSobre, conPlazo, LimitesSesion} from '/sesion.mjs';

const $ = (id) => document.getElementById(id);
let token = new URLSearchParams(location.hash.slice(1)).get('token') || '';
try { token ||= sessionStorage.getItem('vera-lab-token') || ''; if (token) sessionStorage.setItem('vera-lab-token', token); } catch { /* El enlace con token sigue funcionando sin almacenamiento. */ }
history.replaceState(null, '', location.pathname);
let ocupada = true, grabando = false, grabacion = null, ultimoTexto = '', clipURL = '', demo = false, voces = [];
const limites = new LimitesSesion();
let turnoActivo = null, preguntaPendiente = '', audioParcial = false;
let audioPendiente = null;
let vozNatural = false, peticionVoz = null, audioVoz = null, vozURL = '', cacheVoz = '', vozElegida = '';
let vozFlujo = false, vozLocal = false, vozCartesia = false, contextoSalida = null, reproductorVoz = null, tiempos = {};
const hayVoz = 'speechSynthesis' in window;
function estado(s) { $('estado').textContent = s; }
function fallo(s = '') { $('error').textContent = s; $('error').hidden = !s; }
function actualizarNotaVoz() {
  const seleccion = $('voz').value;
  $('nota-voz').textContent = seleccion.startsWith('cartesia:') ? 'Catalina · voz colombiana de Cartesia. Consume caracteres al generar; repetir el último audio usa la caché. Su cuota es independiente de Gemini.' : seleccion.startsWith('local:') ? 'Dora · voz neural en tu equipo, sin cuota de Google ni Cartesia.' : seleccion.startsWith('gemini:') ? `Voz natural · Gemini. Generar audio utiliza una llamada; repetir la misma voz reutiliza el audio.${vozLocal ? ' Si Gemini falla antes de comenzar, Dora local lee la respuesta.' : ''}` : 'Voz del navegador. No consume llamadas a Gemini ni Cartesia.';
}
function controles(b) {
  ocupada = b;
  for (const id of ['enviar', 'pregunta', 'ejemplo']) $(id).disabled = b || limites.bloqueada();
  $('nueva').disabled = b;
  $('microfono').disabled = (b && !grabando) || limites.bloqueada() || !navigator.mediaDevices?.getUserMedia || !window.AudioWorkletNode;
  $('escuchar').disabled = b || !ultimoTexto || (!vozNatural && !hayVoz);
  $('voz').disabled = b;
  $('reintentar').hidden = !preguntaPendiente;
  $('reintentar').disabled = b || limites.bloqueada();
  $('reintentar-audio').hidden = !audioPendiente;
  $('reintentar-audio').disabled = b || limites.bloqueada();
  $('cancelar').hidden = !b || grabando || (!turnoActivo && !peticionVoz);
  const chat = limites.esperaChat(), voz = limites.esperaVoz(), cartesia = limites.esperaCartesia();
  $('espera').hidden = !chat && !voz && !cartesia;
  $('espera').textContent = [chat ? `Chat disponible para reintentar en ${chat} s.` : '', voz ? `Voz de Gemini en pausa: ${voz} s.${vozCartesia ? ' Puedes usar Catalina con la cuota de Cartesia.' : vozLocal ? ' Dora local sigue disponible, sin cuota de Google.' : ' Puedes usar la voz del navegador.'}` : '', cartesia ? `Catalina en pausa: ${cartesia} s. El chat sigue disponible.` : ''].filter(Boolean).join(' ');
}
function actualizar(e) {
  limites.actualizar(e);
  $('modo').textContent = e.demo ? 'Demo local · sin cuota' : 'Gemini · datos ficticios';
  $('cuota').textContent = `${e.turnos || 0} turnos · ${e.usadas}/${e.maximo} solicitudes${e.demo ? ' simuladas' : ' a Google'}`;
  $('modelo').textContent = `Modelo: ${e.modelo}`; $('version').textContent = `Instrucciones: ${e.version}`;
  demo = e.demo;
  vozNatural = e.voz_natural;
  vozLocal = Boolean(e.voz_local);
  vozCartesia = Boolean(e.voz_cartesia);
  vozFlujo = e.voz_flujo && Boolean(window.AudioContext);
  for (const opcion of $('voz').options) if (opcion.value.startsWith('gemini:')) opcion.disabled = !vozNatural;
  for (const opcion of $('voz').options) if (opcion.value.startsWith('local:')) opcion.disabled = !vozLocal;
  for (const opcion of $('voz').options) if (opcion.value.startsWith('cartesia:')) opcion.disabled = !vozCartesia;
  if (!vozCartesia && $('voz').value.startsWith('cartesia:')) $('voz').value = vozNatural && !e.demo ? 'gemini:Aoede' : vozLocal ? 'local:Dora' : 'navegador';
  if (!vozNatural && $('voz').value.startsWith('gemini:')) $('voz').value = 'navegador';
  if (e.demo && vozLocal && $('voz').value.startsWith('gemini:')) $('voz').value = 'local:Dora';
  actualizarNotaVoz();
  const v = e.voces;
  if (v) $('estadistica-voz').textContent = `Generaciones completas: ${v.gemini + v.local + (v.cartesia || 0)}/${v.solicitudes - v.cache} · Gemini ${v.gemini} · Catalina ${v.cartesia || 0} · Dora ${v.local} · Repeticiones en caché ${v.cache}.${vozCartesia ? ` Cartesia: ${e.caracteres_cartesia}/${e.max_caracteres_cartesia} caracteres reservados en esta sesión; no es el saldo de la cuenta.` : ''} No mide reproducción ni calidad percibida.`;
  if (demo) $('aviso').textContent = 'Demo sin llamadas a Google. La pregunta y la transcripción de ejemplo son fijas: «¿Cuánto tengo?». Puedes probar también la grabación.';
  $('nota-audio').textContent = demo ? 'Hasta 20 s · prueba local, sin enviar a Google' : 'Hasta 20 s · al detener, se envía a Gemini';
  $('pregunta').readOnly = demo;
  if (demo) $('pregunta').value = '¿Cuánto tengo?';
  if (e.usadas >= e.maximo) estado('Límite local de sesión alcanzado. El texto y la voz del navegador siguen disponibles.');
}
async function api(ruta, opciones = {}) {
  try {
    return await conPlazo(async (signal) => {
      const r = await fetch(ruta, {...opciones, signal, headers: {'X-Vera-Token': token, ...opciones.headers}});
      let sobre; try { sobre = await r.json(); } catch { throw new ErrorLaboratorio('El servidor no devolvió una respuesta válida. Puedes volver a intentar.'); }
      if (!r.ok || !sobre.success) throw errorDelSobre(sobre, r.headers.get('Retry-After'));
      return sobre.data;
    }, opciones.signal || turnoActivo?.signal, ruta === '/api/estado' ? 4000 : 90000);
  } catch (e) { if (e instanceof TypeError) e = new ErrorLaboratorio('No se pudo conectar al laboratorio. Comprueba que la consola siga abierta.'); limites.registrar(e, ruta.startsWith('/api/voz')); throw e; }
}
function mensaje(rol, texto, backend = false) {
  $('bienvenida').hidden = true;
  const bloque = document.createElement('article'); bloque.className = `mensaje ${rol}`;
  const titulo = document.createElement('strong'); titulo.textContent = rol === 'usuario' ? 'TÚ' : backend ? 'VERA · RESPUESTA DEL BACKEND' : 'VERA';
  const cuerpo = document.createElement('p'); cuerpo.textContent = texto;
  bloque.append(titulo, cuerpo); $('mensajes').append(bloque); $('mensajes').scrollTop = $('mensajes').scrollHeight;
}
function traza(eventos) {
  $('eventos').replaceChildren(); const funciones = eventos.filter((e) => e.tipo === 'funcion').length;
  $('cantidad-traza').textContent = funciones ? `${funciones} función${funciones > 1 ? 'es' : ''}` : 'Sin llamadas';
  if (!eventos.length) { const p = document.createElement('p'); p.className = 'vacio'; p.textContent = 'Vera respondió sin utilizar herramientas.'; $('eventos').append(p); }
  for (const e of eventos) {
    const bloque = document.createElement('details'); bloque.className = `evento ${e.tipo}`; bloque.open = true;
    const titulo = document.createElement('summary'); titulo.textContent = e.nombre;
    const tipo = document.createElement('span'); tipo.textContent = e.tipo === 'funcion' ? 'Función elegida · argumentos' : 'Backend de prueba · resultado validado'; titulo.append(tipo);
    const pre = document.createElement('pre'); pre.textContent = JSON.stringify(e.datos, null, 2);
    bloque.append(titulo, pre); $('eventos').append(bloque);
  }
}
function detenerVoz() { peticionVoz?.abort(); reproductorVoz?.detener(); reproductorVoz = null; if (audioVoz) { audioVoz.pause(); audioVoz.currentTime = 0; } if (hayVoz) speechSynthesis.cancel(); $('silenciar').disabled = true; }
function limpiarVoz() { detenerVoz(); if (vozURL) URL.revokeObjectURL(vozURL); vozURL = ''; audioVoz = null; cacheVoz = ''; vozElegida = ''; $('resultado-voz').textContent = ''; }
function medir(nombre, inicio) {
  tiempos[nombre] = performance.now() - inicio;
  $('tiempos').hidden = false; $('tiempos').textContent = Object.entries(tiempos).map(([n, ms]) => `${n}: ${(ms / 1000).toFixed(1)} s`).join(' · ');
}
function nuevoTiempo() { tiempos = {}; $('tiempos').hidden = true; }
function prepararSalida() {
  if (!vozFlujo) return;
  contextoSalida ||= new AudioContext();
  if (contextoSalida.state === 'suspended') contextoSalida.resume().catch(() => {});
}
function conservarAudio(bytes, voz) {
  if (vozURL) URL.revokeObjectURL(vozURL);
  vozURL = URL.createObjectURL(new Blob([bytes], {type: 'audio/wav'}));
  audioVoz = new Audio(vozURL); cacheVoz = ultimoTexto; vozElegida = voz;
  audioVoz.onended = () => { $('silenciar').disabled = true; if (!ocupada && !grabando) estado('Listo. Puedes continuar o volver a escuchar la respuesta.'); };
  audioVoz.onerror = () => { $('silenciar').disabled = true; fallo('No se pudo reproducir el audio. Puedes volver a pulsar Escuchar respuesta.'); };
}
function mostrarFuenteVoz(datos) {
  const nombre = datos.fuente === 'cartesia' ? 'Catalina · Cartesia' : datos.fuente === 'kokoro' ? `Dora · neural local${datos.motivo_respaldo ? ' · respaldo de Gemini' : ''}` : `Gemini · ${$('voz').value.split(':')[1]}`;
  $('resultado-voz').textContent = `Voz: ${nombre}${datos.cache ? ' · audio guardado en memoria' : ''}.`;
}
async function recibirVoz(voz, solicitud) {
  prepararSalida();
  if (!contextoSalida || contextoSalida.state !== 'running') throw new DOMException('Pulsa Escuchar respuesta para habilitar el audio del navegador.', 'NotAllowedError');
  const inicio = performance.now(), partes = [];
  let primera = true, recibioFin = false;
  reproductorVoz = new ReproductorPCM(contextoSalida, () => { $('silenciar').disabled = true; if (!ocupada && !grabando) estado('Listo. Puedes continuar o volver a escuchar la respuesta.'); });
  await conPlazo(async (signal) => {
  const r = await fetch('/api/voz/flujo', {method: 'POST', headers: {'X-Vera-Token': token, 'Content-Type': 'text/plain'}, body: voz, signal});
  await leerVozFlujo(r, async (e) => {
    if (solicitud.signal.aborted) throw new DOMException('Voz detenida', 'AbortError');
    if (e.tipo === 'audio') {
      const bytes = Uint8Array.from(atob(e.pcm), (c) => c.charCodeAt(0)); partes.push(bytes);
      reproductorVoz.agregar(bytes);
      audioParcial = true;
      if (primera) { primera = false; medir('Primera voz', inicio); estado('Vera está respondiendo mientras llega el resto del audio.'); }
    } else if (e.tipo === 'wav') {
      conservarAudio(Uint8Array.from(atob(e.audio), (c) => c.charCodeAt(0)), voz);
      await audioVoz.play(); primera = false; medir('Primera voz', inicio);
      audioParcial = true;
    } else if (e.tipo === 'fin') {
      actualizar(e.estado); mostrarFuenteVoz(e); recibioFin = true;
    }
  });
  }, solicitud.signal, 55000);
  if (!recibioFin || primera) throw new Error('La voz no se completó.');
  if (partes.length) { conservarAudio(wavPCM(partes), voz); reproductorVoz.finalizar(); }
}
async function escuchar() {
  if (!ultimoTexto || ocupada) return;
  detenerVoz(); fallo(); audioParcial = false;
  if (vozNatural && ($('voz').value.startsWith('gemini:') || $('voz').value.startsWith('local:') || $('voz').value.startsWith('cartesia:'))) {
    const solicitud = new AbortController(); peticionVoz = solicitud;
    controles(true); $('silenciar').disabled = false;
    let completa = false;
    try {
      const voz = $('voz').value.split(':')[1];
      if (cacheVoz !== ultimoTexto || vozElegida !== voz || !audioVoz) {
        if (voz === 'Catalina' && (limites.cartesiaAgotada || limites.cartesiaCredenciales || limites.esperaCartesia())) throw new ErrorLaboratorio('Catalina no está disponible ahora; no se consumió otra llamada. Puedes elegir otra voz.', 'CartesiaUnavailable');
        if (voz !== 'Catalina' && !vozLocal && (limites.agotada || limites.credenciales || limites.vozNoDisponible || limites.esperaVoz())) throw new ErrorLaboratorio('La voz natural no está disponible ahora; no se consumió otra llamada.', 'VoiceUnavailable');
        estado('Generando la voz natural de Vera…');
        if (vozFlujo && contextoSalida?.state === 'running') { await recibirVoz(voz, solicitud); completa = true; return; }
        const inicioVoz = performance.now();
        const datos = await api('/api/voz', {method: 'POST', headers: {'Content-Type': 'text/plain'}, body: voz, signal: solicitud.signal});
        actualizar(datos.estado);
        mostrarFuenteVoz(datos);
        const bytes = Uint8Array.from(atob(datos.audio), (c) => c.charCodeAt(0));
        conservarAudio(bytes, voz); medir('Primera voz', inicioVoz);
      }
      if (solicitud.signal.aborted) return;
      await audioVoz.play(); estado('Vera está respondiendo. Puedes detener la voz o continuar.');
      completa = true;
    } catch (e) {
      limites.registrar(e, true);
      reproductorVoz?.detener(); reproductorVoz = null;
      if (e.name !== 'AbortError') fallo(e.name === 'NotAllowedError' ? 'Tu navegador requiere un clic para reproducir. Pulsa Escuchar respuesta; el audio ya está generado.' : `${e.message} La respuesta escrita sigue disponible. Puedes elegir Voz del navegador.`);
      estado('La respuesta escrita está lista. Puedes volver a escuchar o continuar.');
      $('silenciar').disabled = true;
      if (e.name !== 'AbortError' && e.name !== 'NotAllowedError' && !audioParcial && $('respaldo').checked && hayVoz) {
        leerNavegador();
        $('resultado-voz').textContent = 'Voz: navegador · respaldo final. No cuenta como voz neural completada.';
        estado('La voz natural no está disponible. Vera está leyendo con la voz del navegador.');
      }
    } finally { peticionVoz = null; if (!completa) await refrescarConsumo(); controles(false); }
    return;
  }
  leerNavegador();
}
function leerNavegador() {
  if (!hayVoz) { fallo('Este navegador no ofrece síntesis de voz local.'); return; }
  $('resultado-voz').textContent = 'Voz: navegador. No cuenta como voz neural completada.';
  const frase = new SpeechSynthesisUtterance(ultimoTexto); frase.lang = 'es-CO';
  frase.voice = voces.find((v) => v.voiceURI === $('voz').value) || voces.find((v) => v.lang.startsWith('es')) || null;
  frase.onend = () => { $('silenciar').disabled = true; };
  frase.onerror = (e) => { $('silenciar').disabled = true; if (!['canceled', 'interrupted'].includes(e.error)) fallo('No se pudo leer la respuesta. Prueba otra voz del navegador.'); };
  $('silenciar').disabled = false; speechSynthesis.speak(frase);
}
function cargarVoces() {
  if (!hayVoz) return;
  const previa = $('voz').value;
  voces = speechSynthesis.getVoices().sort((a, b) => Number(b.lang.startsWith('es')) - Number(a.lang.startsWith('es')));
  for (const opcion of [...$('voz').options]) if (!opcion.value.startsWith('gemini:') && !opcion.value.startsWith('local:') && !opcion.value.startsWith('cartesia:') && opcion.value !== 'navegador') opcion.remove();
  for (const v of voces) $('voz').add(new Option(`${v.name} · ${v.lang}`, v.voiceURI));
  const preferida = voces.find((v) => v.voiceURI === previa) || voces.find((v) => v.lang.toLowerCase() === 'es-co') || voces.find((v) => v.lang.startsWith('es'));
  if (previa.startsWith('gemini:') || previa.startsWith('local:') || previa.startsWith('cartesia:') || previa === 'navegador') $('voz').value = previa;
  else if (preferida) $('voz').value = preferida.voiceURI;
}
async function preguntar(texto, reintento = false) {
  preguntaPendiente = texto;
  if (!reintento) mensaje('usuario', texto); $('eventos').replaceChildren(); $('cantidad-traza').textContent = 'Consultando…';
  estado('Vera está consultando.');
  const inicioRespuesta = performance.now();
  const datos = await api('/api/preguntar', {method: 'POST', headers: {'Content-Type': 'text/plain; charset=utf-8'}, body: texto});
  medir('Respuesta', inicioRespuesta);
  actualizar(datos.estado); traza(datos.eventos); mensaje('vera', datos.texto, datos.respuesta_del_backend); ultimoTexto = datos.texto;
  if (datos.motivo_recuperacion) fallo(`El modelo no completó este turno (${datos.motivo_recuperacion}). Respondió el backend; puedes continuar conversando.`);
  preguntaPendiente = '';
  if (datos.accion_pendiente) {
    const a = datos.accion_pendiente, tarjeta = document.createElement('div'); tarjeta.className = 'tarjeta';
    const titulo = document.createElement('strong'); titulo.textContent = 'Propuesta simulada';
    const p = document.createElement('p'); p.textContent = `${a.monto} · ${a.destinatario}`;
    const nota = document.createElement('p'); nota.textContent = 'Pendiente de confirmación en la app. No se ejecutó dinero.';
    tarjeta.append(titulo, p, nota); $('mensajes').append(tarjeta);
  }
  if (!limites.bloqueada()) estado('Listo. Puedes continuar la conversación.');
}
async function enviarTexto(texto, reintento = false) {
  if (ocupada || limites.bloqueada() || !texto.trim()) return;
  turnoActivo = new AbortController(); controles(true); fallo(); limpiarVoz(); ultimoTexto = '';
  audioPendiente = null;
  if (!reintento) { $('transcripcion').hidden = true; $('texto-transcripcion').textContent = ''; }
  nuevoTiempo(); prepararSalida();
  let respondio = false;
  try { await preguntar(demo ? '¿Cuánto tengo?' : texto.trim(), reintento); respondio = true; if (!demo) $('pregunta').value = ''; }
  catch (e) { $('cantidad-traza').textContent = 'Turno incompleto'; if (e.name !== 'AbortError') fallo(e.message); estado(e.name === 'AbortError' ? 'Consulta cancelada. Puedes reintentar la respuesta.' : 'La consulta no se completó. Puedes reintentar la respuesta.'); }
  finally { turnoActivo = null; if (!respondio) await refrescarConsumo(); controles(false); if (respondio && $('automatica').checked) await escuchar(); }
}
async function refrescarConsumo() { try { actualizar(await api('/api/estado')); } catch { /* Conserva el error original si el servidor se desconectó. */ } }
$('formulario').addEventListener('submit', (e) => { e.preventDefault(); enviarTexto($('pregunta').value); });
$('pregunta').addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); enviarTexto($('pregunta').value); } });
$('ejemplo').addEventListener('click', () => enviarTexto('¿Cuánto tengo disponible?'));
$('reintentar').addEventListener('click', () => enviarTexto(preguntaPendiente, true));
$('reintentar-audio').addEventListener('click', async () => {
  if (ocupada || limites.bloqueada() || !audioPendiente) return;
  turnoActivo = new AbortController(); controles(true); fallo(); nuevoTiempo();
  let respondio = false, transcrito = false;
  try { const texto = await transcribirClip(audioPendiente); transcrito = true; await preguntar(texto); respondio = true; }
  catch (e) { if (e.name !== 'AbortError') fallo(e.message); estado(transcrito ? 'El audio se transcribió. Puedes reintentar sólo la respuesta.' : 'No se pudo transcribir. Puedes reintentar este audio, escribir o grabar de nuevo.'); }
  finally { turnoActivo = null; if (!respondio) await refrescarConsumo(); controles(false); if (respondio && $('automatica').checked) await escuchar(); }
});
$('cancelar').addEventListener('click', () => { turnoActivo?.abort(); detenerVoz(); estado('Cancelando la consulta…'); });
$('escuchar').addEventListener('click', escuchar); $('silenciar').addEventListener('click', () => { detenerVoz(); estado('Voz detenida. La respuesta escrita sigue disponible.'); });
$('voz').addEventListener('change', actualizarNotaVoz);
$('nueva').addEventListener('click', async () => {
  if (ocupada) return; controles(true); fallo(); limpiarVoz();
  try {
    actualizar(await api('/api/nueva', {method: 'POST'}));
    for (const nodo of $('mensajes').querySelectorAll('.mensaje,.tarjeta')) nodo.remove(); $('bienvenida').hidden = false;
    $('eventos').replaceChildren(); $('cantidad-traza').textContent = 'Sin llamadas'; $('transcripcion').hidden = true;
    $('clip').hidden = true; if (clipURL) URL.revokeObjectURL(clipURL); clipURL = ''; ultimoTexto = '';
    nuevoTiempo();
    preguntaPendiente = '';
    audioPendiente = null;
    estado('Conversación nueva. El consumo y las esperas de cuota se conservan.');
  } catch (e) { fallo(e.message); } finally { controles(false); }
});
async function transcribirClip(blob) {
  estado('Transcribiendo tu audio…');
  const inicio = performance.now();
  const datos = await api('/api/transcribir', {method: 'POST', headers: {'Content-Type': 'audio/wav'}, body: blob});
  medir('Transcripción', inicio); actualizar(datos.estado);
  $('texto-transcripcion').textContent = datos.transcripcion; $('transcripcion').hidden = false;
  $('pregunta').value = datos.transcripcion; audioPendiente = null;
  return datos.transcripcion;
}
async function detenerGrabacion() {
  const g = grabacion; if (!g || !grabando) return;
  grabando = false; controles(true); clearInterval(g.temporizador); clearTimeout(g.limite);
  turnoActivo = new AbortController(); controles(true);
  $('reloj').hidden = true; $('microfono').classList.remove('activo'); $('texto-microfono').textContent = 'Hablar'; $('estado').classList.remove('grabando');
  let respondio = false;
  let transcrito = false;
  try {
    g.nodo.port.postMessage('fin'); await Promise.race([g.fin, new Promise((r) => setTimeout(r, 500))]);
    g.stream.getTracks().forEach((t) => t.stop()); await g.contexto.close(); grabacion = null;
    const wav = pcmAWav(g.bloques, g.contexto.sampleRate), blob = new Blob([wav], {type: 'audio/wav'});
    if (clipURL) URL.revokeObjectURL(clipURL); clipURL = URL.createObjectURL(blob); $('clip').src = clipURL; $('clip').hidden = false;
    audioPendiente = blob;
    const texto = await transcribirClip(blob);
    transcrito = true;
    await preguntar(texto);
    respondio = true;
  } catch (e) { $('cantidad-traza').textContent = 'Turno incompleto'; if (e.name !== 'AbortError') fallo(e.message); estado(e.name === 'AbortError' ? 'Consulta cancelada.' : transcrito ? 'Tu audio se transcribió correctamente. Puedes reintentar sólo la respuesta.' : 'No se pudo transcribir el audio. Puedes reintentar este clip sin volver a grabarlo, escribir o grabar otra vez.'); }
  finally { turnoActivo = null; g.stream.getTracks().forEach((t) => t.stop()); if (g.contexto.state !== 'closed') await g.contexto.close(); grabacion = null; if (!respondio) await refrescarConsumo(); controles(false); if (respondio && $('automatica').checked) await escuchar(); }
}
$('microfono').addEventListener('click', async () => {
  if (grabando) { await detenerGrabacion(); return; } if (ocupada) return;
  controles(true); fallo(); limpiarVoz(); ultimoTexto = ''; estado('Permite el micrófono en tu navegador.');
  $('transcripcion').hidden = true; $('texto-transcripcion').textContent = '';
  $('clip').hidden = true; $('clip').removeAttribute('src');
  if (clipURL) URL.revokeObjectURL(clipURL); clipURL = '';
  preguntaPendiente = '';
  audioPendiente = null;
  nuevoTiempo(); prepararSalida();
  let stream, contexto;
  try {
    stream = await navigator.mediaDevices.getUserMedia({audio: {channelCount: 1, echoCancellation: true, noiseSuppression: true}});
    contexto = new AudioContext(); await contexto.resume(); await contexto.audioWorklet.addModule('/grabador.js');
    const fuente = contexto.createMediaStreamSource(stream), nodo = new AudioWorkletNode(contexto, 'grabador-vera');
    const silencio = contexto.createGain(); silencio.gain.value = 0; fuente.connect(nodo); nodo.connect(silencio).connect(contexto.destination);
    const bloques = []; let cerrar; const fin = new Promise((r) => { cerrar = r; });
    nodo.port.onmessage = (e) => { if (e.data.pcm) bloques.push(e.data.pcm); if (e.data.fin) cerrar(); };
    const inicio = performance.now(); grabando = true;
    grabacion = {stream, contexto, nodo, bloques, fin, temporizador: setInterval(() => { $('reloj').textContent = `00:${String(Math.min(20, Math.floor((performance.now() - inicio) / 1000))).padStart(2, '0')}`; }, 200), limite: setTimeout(detenerGrabacion, 20000)};
    $('reloj').textContent = '00:00'; $('reloj').hidden = false; $('microfono').classList.add('activo'); $('texto-microfono').textContent = 'Detener y enviar'; $('estado').classList.add('grabando'); estado('Grabando. Pulsa «Detener y enviar» al terminar.'); controles(true);
  } catch (e) {
    stream?.getTracks().forEach((t) => t.stop()); if (contexto && contexto.state !== 'closed') await contexto.close();
    fallo(e.name === 'NotAllowedError' ? 'No se concedió acceso al micrófono. Habilítalo en el navegador o escribe tu pregunta.' : 'No se pudo iniciar el micrófono. Comprueba el dispositivo y abre esta página desde localhost.'); estado('Puedes continuar escribiendo.'); controles(false);
  }
});
const relojCuota = setInterval(() => controles(ocupada), 1000);
window.addEventListener('pagehide', () => { clearInterval(relojCuota); turnoActivo?.abort(); limpiarVoz(); contextoSalida?.close(); if (clipURL) URL.revokeObjectURL(clipURL); if (grabacion) { clearTimeout(grabacion.limite); clearInterval(grabacion.temporizador); grabacion.stream.getTracks().forEach((t) => t.stop()); grabacion.contexto.close(); } });
cargarVoces(); if (hayVoz) speechSynthesis.addEventListener('voiceschanged', cargarVoces);
try { actualizar(await api('/api/estado')); estado('Listo. Escribe o pulsa «Hablar».'); controles(false); }
catch (e) { fallo(e.message || 'No se pudo conectar. Abre el enlace completo del lanzador.'); estado('Sesión sin conectar.'); }
