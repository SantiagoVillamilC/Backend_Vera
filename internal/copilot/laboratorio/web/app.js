import {pcmAWav} from '/audio.mjs';

const $ = (id) => document.getElementById(id);
let token = new URLSearchParams(location.hash.slice(1)).get('token') || '';
try { token ||= sessionStorage.getItem('vera-lab-token') || ''; if (token) sessionStorage.setItem('vera-lab-token', token); } catch { /* El enlace con token sigue funcionando sin almacenamiento. */ }
history.replaceState(null, '', location.pathname);
let ocupada = true, grabando = false, grabacion = null, ultimoTexto = '', clipURL = '', demo = false, voces = [];
let detenidoPorCuota = false;
const hayVoz = 'speechSynthesis' in window;
function estado(s) { $('estado').textContent = s; }
function fallo(s = '') { $('error').textContent = s; $('error').hidden = !s; }
function controles(b) {
  ocupada = b;
  for (const id of ['enviar', 'pregunta', 'nueva', 'ejemplo']) $(id).disabled = b || detenidoPorCuota;
  $('microfono').disabled = (b && !grabando) || detenidoPorCuota || !navigator.mediaDevices?.getUserMedia || !window.AudioWorkletNode;
  $('escuchar').disabled = b || !ultimoTexto || !hayVoz;
}
function actualizar(e) {
  $('modo').textContent = e.demo ? 'Demo local · sin cuota' : 'Gemini · datos ficticios';
  $('cuota').textContent = `${e.usadas}/${e.maximo} llamadas${e.demo ? ' simuladas' : ''}`;
  $('modelo').textContent = `Modelo: ${e.modelo}`; $('version').textContent = `Instrucciones: ${e.version}`;
  demo = e.demo;
  if (demo) $('aviso').textContent = 'Demo sin llamadas a Google. La pregunta y la transcripción de ejemplo son fijas: «¿Cuánto tengo?». Puedes probar también la grabación.';
  $('nota-audio').textContent = demo ? 'Hasta 20 s · prueba local, sin enviar a Google' : 'Hasta 20 s · al detener, se envía a Gemini';
  $('pregunta').readOnly = demo;
  if (demo) $('pregunta').value = '¿Cuánto tengo?';
  if (e.usadas >= e.maximo) { detenidoPorCuota = true; estado('Límite de sesión alcanzado. Reinicia el laboratorio para otra sesión.'); }
}
async function api(ruta, opciones = {}) {
  const r = await fetch(ruta, {...opciones, headers: {'X-Vera-Token': token, ...opciones.headers}});
  const sobre = await r.json();
  if (!r.ok || !sobre.success) {
    if (['SessionLimit', 'ProviderLimit', 'InvalidCredentials'].includes(sobre.error?.code)) detenidoPorCuota = true;
    throw new Error(sobre.error?.message || 'No se pudo completar la consulta.');
  }
  return sobre.data;
}
function mensaje(rol, texto) {
  $('bienvenida').hidden = true;
  const bloque = document.createElement('article'); bloque.className = `mensaje ${rol}`;
  const titulo = document.createElement('strong'); titulo.textContent = rol === 'usuario' ? 'TÚ' : 'VERA';
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
function detenerVoz() { if (hayVoz) speechSynthesis.cancel(); $('silenciar').disabled = true; }
function escuchar() {
  if (!hayVoz || !ultimoTexto || ocupada) return;
  detenerVoz(); const frase = new SpeechSynthesisUtterance(ultimoTexto); frase.lang = 'es-CO';
  frase.voice = voces.find((v) => v.voiceURI === $('voz').value) || null;
  frase.onend = () => { $('silenciar').disabled = true; };
  frase.onerror = (e) => { $('silenciar').disabled = true; if (!['canceled', 'interrupted'].includes(e.error)) fallo('No se pudo leer la respuesta. Prueba otra voz del navegador.'); };
  $('silenciar').disabled = false; speechSynthesis.speak(frase);
}
function cargarVoces() {
  if (!hayVoz) { $('automatica').disabled = true; $('voz').disabled = true; return; }
  const previa = $('voz').value;
  voces = speechSynthesis.getVoices().sort((a, b) => Number(b.lang.startsWith('es')) - Number(a.lang.startsWith('es')));
  $('voz').replaceChildren(new Option('Voz del navegador', ''));
  for (const v of voces) $('voz').add(new Option(`${v.name} · ${v.lang}`, v.voiceURI));
  const preferida = voces.find((v) => v.voiceURI === previa) || voces.find((v) => v.lang.toLowerCase() === 'es-co') || voces.find((v) => v.lang.startsWith('es'));
  if (preferida) $('voz').value = preferida.voiceURI;
}
async function preguntar(texto) {
  mensaje('usuario', texto); $('eventos').replaceChildren(); $('cantidad-traza').textContent = 'Consultando…';
  estado('Vera está consultando. Puede haber una pausa entre llamadas para respetar la cuota.');
  const datos = await api('/api/preguntar', {method: 'POST', headers: {'Content-Type': 'text/plain; charset=utf-8'}, body: texto});
  actualizar(datos.estado); traza(datos.eventos); mensaje('vera', datos.texto); ultimoTexto = datos.texto;
  if (datos.accion_pendiente) {
    const a = datos.accion_pendiente, tarjeta = document.createElement('div'); tarjeta.className = 'tarjeta';
    const titulo = document.createElement('strong'); titulo.textContent = 'Propuesta simulada';
    const p = document.createElement('p'); p.textContent = `${a.monto} · ${a.destinatario}`;
    const nota = document.createElement('p'); nota.textContent = 'Pendiente de confirmación en la app. No se ejecutó dinero.';
    tarjeta.append(titulo, p, nota); $('mensajes').append(tarjeta);
  }
  if (!detenidoPorCuota) estado('Listo. Puedes continuar la conversación.');
}
async function enviarTexto(texto) {
  if (ocupada || !texto.trim()) return;
  controles(true); fallo(); detenerVoz(); ultimoTexto = '';
  try { await preguntar(demo ? '¿Cuánto tengo?' : texto.trim()); if (!demo) $('pregunta').value = ''; }
  catch (e) { fallo(e.message); estado('La consulta no se completó.'); }
  finally { await refrescarConsumo(); controles(false); if ($('automatica').checked && !detenidoPorCuota) escuchar(); }
}
async function refrescarConsumo() { try { actualizar(await api('/api/estado')); } catch { /* Conserva el error original si el servidor se desconectó. */ } }
$('formulario').addEventListener('submit', (e) => { e.preventDefault(); enviarTexto($('pregunta').value); });
$('pregunta').addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); enviarTexto($('pregunta').value); } });
$('ejemplo').addEventListener('click', () => enviarTexto('¿Cuánto tengo disponible?'));
$('escuchar').addEventListener('click', escuchar); $('silenciar').addEventListener('click', detenerVoz);
$('nueva').addEventListener('click', async () => {
  if (ocupada) return; controles(true); fallo(); detenerVoz();
  try {
    actualizar(await api('/api/nueva', {method: 'POST'}));
    for (const nodo of $('mensajes').querySelectorAll('.mensaje,.tarjeta')) nodo.remove(); $('bienvenida').hidden = false;
    $('eventos').replaceChildren(); $('cantidad-traza').textContent = 'Sin llamadas'; $('transcripcion').hidden = true;
    $('clip').hidden = true; if (clipURL) URL.revokeObjectURL(clipURL); clipURL = ''; ultimoTexto = '';
    estado(detenidoPorCuota ? 'El límite de solicitudes se conserva.' : 'Conversación nueva. El consumo de solicitudes se conserva.');
  } catch (e) { fallo(e.message); } finally { controles(false); }
});
async function detenerGrabacion() {
  const g = grabacion; if (!g || !grabando) return;
  grabando = false; controles(true); clearInterval(g.temporizador); clearTimeout(g.limite);
  $('reloj').hidden = true; $('microfono').classList.remove('activo'); $('texto-microfono').textContent = 'Hablar'; $('estado').classList.remove('grabando');
  try {
    g.nodo.port.postMessage('fin'); await Promise.race([g.fin, new Promise((r) => setTimeout(r, 500))]);
    g.stream.getTracks().forEach((t) => t.stop()); await g.contexto.close(); grabacion = null;
    const wav = pcmAWav(g.bloques, g.contexto.sampleRate), blob = new Blob([wav], {type: 'audio/wav'});
    if (clipURL) URL.revokeObjectURL(clipURL); clipURL = URL.createObjectURL(blob); $('clip').src = clipURL; $('clip').hidden = false;
    estado('Transcribiendo tu audio…');
    const datos = await api('/api/transcribir', {method: 'POST', headers: {'Content-Type': 'audio/wav'}, body: blob});
    actualizar(datos.estado); $('texto-transcripcion').textContent = datos.transcripcion; $('transcripcion').hidden = false;
    await preguntar(datos.transcripcion);
  } catch (e) { fallo(e.message); estado('No se pudo completar el turno de voz. Puedes escribir o grabar otra vez.'); }
  finally { g.stream.getTracks().forEach((t) => t.stop()); if (g.contexto.state !== 'closed') await g.contexto.close(); grabacion = null; await refrescarConsumo(); controles(false); if ($('automatica').checked && !detenidoPorCuota) escuchar(); }
}
$('microfono').addEventListener('click', async () => {
  if (grabando) { await detenerGrabacion(); return; } if (ocupada) return;
  controles(true); fallo(); detenerVoz(); ultimoTexto = ''; estado('Permite el micrófono en tu navegador.');
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
window.addEventListener('pagehide', () => { detenerVoz(); if (clipURL) URL.revokeObjectURL(clipURL); if (grabacion) { clearTimeout(grabacion.limite); clearInterval(grabacion.temporizador); grabacion.stream.getTracks().forEach((t) => t.stop()); grabacion.contexto.close(); } });
cargarVoces(); if (hayVoz) speechSynthesis.addEventListener('voiceschanged', cargarVoces);
try { actualizar(await api('/api/estado')); estado('Listo. Escribe o pulsa «Hablar».'); controles(false); }
catch (e) { fallo(e.message || 'No se pudo conectar. Abre el enlace completo del lanzador.'); estado('Sesión sin conectar.'); }
