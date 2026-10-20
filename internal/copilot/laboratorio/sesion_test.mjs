import {test} from 'node:test';
import assert from 'node:assert/strict';
import {conPlazo, ErrorLaboratorio, errorDelSobre, LimitesSesion} from './web/sesion.mjs';

test('la cuota por minuto expira sin reiniciar y la voz no bloquea el chat', () => {
  let ahora = 1000; const s = new LimitesSesion(() => ahora);
  s.registrar(new ErrorLaboratorio('Cuota', 'ProviderMinuteLimit', 2));
  assert.equal(s.bloqueada(), true); ahora += 2100; assert.equal(s.bloqueada(), false);
  s.registrar(new ErrorLaboratorio('Cuota', 'ProviderDailyLimit', 3600), true);
  assert.equal(s.esperaVoz(), 3600); assert.equal(s.bloqueada(), false);
  s.registrar(new ErrorLaboratorio('Permiso TTS', 'InvalidCredentials'), true);
  assert.equal(s.vozNoDisponible, true); assert.equal(s.bloqueada(), false);
  s.actualizar({usadas: 20, maximo: 20, espera_chat_segundos: 0, espera_voz_segundos: 3600});
  assert.equal(s.agotada, true); assert.equal(s.esperaVoz(), 3600);
});
test('Retry-After conserva la espera del servidor y acota datos inválidos', () => {
  assert.equal(errorDelSobre({error: {code: 'ProviderMinuteLimit', message: 'Espera'}}, '3').reintentarEn, 3);
  assert.equal(errorDelSobre({}, '9999999999').reintentarEn, 60);
});
test('el timeout cubre la espera del cuerpo y libera la operación', async () => {
  await assert.rejects(conPlazo((signal) => new Promise((resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Abortado', 'AbortError')))), null, 5), (e) => e.codigo === 'Timeout');
  assert.equal(await conPlazo(async () => 'siguiente', null, 50), 'siguiente');
});
test('cancelar se distingue del timeout para no activar lectura de respaldo', async () => {
  const c = new AbortController();
  const p = conPlazo((signal) => new Promise((resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('Cancelado', 'AbortError')))), c.signal, 1000);
  c.abort(); await assert.rejects(p, (e) => e.name === 'AbortError' && !e.codigo);
});
