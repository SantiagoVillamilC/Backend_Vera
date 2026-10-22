export class ErrorLaboratorio extends Error {
  constructor(mensaje, codigo = 'Unavailable', reintentarEn = 0) { super(mensaje); this.codigo = codigo; this.reintentarEn = reintentarEn; }
}

export function errorDelSobre(sobre, retryAfter = null) {
  const segundos = Number(retryAfter);
  return new ErrorLaboratorio(sobre?.error?.message || 'No se pudo completar la solicitud.', sobre?.error?.code || 'Unavailable', Number.isFinite(segundos) && segundos > 0 && segundos <= 86400 ? segundos : 60);
}

// El plazo cubre también la lectura del cuerpo/stream, no sólo las cabeceras.
export async function conPlazo(ejecutar, externa, milisegundos = 90000) {
  const control = new AbortController(); let vencido = false;
  const abortar = () => control.abort();
  if (externa?.aborted) abortar(); else externa?.addEventListener('abort', abortar, {once: true});
  const reloj = setTimeout(() => { vencido = true; control.abort(); }, milisegundos);
  try { return await ejecutar(control.signal); }
  catch (e) { if (vencido) throw new ErrorLaboratorio('Se agotó el tiempo de espera. Puedes reintentar el paso pendiente.', 'Timeout'); throw e; }
  finally { clearTimeout(reloj); externa?.removeEventListener('abort', abortar); }
}

export class LimitesSesion {
  constructor(ahora = () => Date.now()) { this.ahora = ahora; this.agotada = false; this.credenciales = false; this.vozNoDisponible = false; this.chatHasta = 0; this.vozHasta = 0; this.cartesiaHasta = 0; this.cartesiaAgotada = false; this.cartesiaCredenciales = false; }
  actualizar(e) {
    this.agotada = e.usadas >= e.maximo;
    if (Number.isFinite(e.espera_chat_segundos)) this.chatHasta = this.ahora() + e.espera_chat_segundos * 1000;
    if (Number.isFinite(e.espera_voz_segundos)) this.vozHasta = this.ahora() + e.espera_voz_segundos * 1000;
    if (Number.isFinite(e.espera_cartesia_segundos)) this.cartesiaHasta = this.ahora() + e.espera_cartesia_segundos * 1000;
    if (e.max_caracteres_cartesia > 0) this.cartesiaAgotada ||= e.caracteres_cartesia >= e.max_caracteres_cartesia;
  }
  registrar(e, voz = false) {
    if (e.codigo === 'CartesiaSessionLimit') this.cartesiaAgotada = true;
    if (e.codigo === 'CartesiaCredentials') this.cartesiaCredenciales = true;
    if (e.codigo === 'CartesiaLimit') this.cartesiaHasta = this.ahora() + (e.reintentarEn > 0 && e.reintentarEn <= 86400 ? e.reintentarEn : 60) * 1000;
    if (e.codigo === 'SessionLimit') this.agotada = true;
    if (e.codigo === 'InvalidCredentials') { if (voz) this.vozNoDisponible = true; else this.credenciales = true; }
    if (['ProviderLimit', 'ProviderMinuteLimit', 'ProviderDailyLimit'].includes(e.codigo)) {
      const espera = e.reintentarEn > 0 && e.reintentarEn <= 86400 ? e.reintentarEn : 60;
      if (voz) this.vozHasta = this.ahora() + espera * 1000; else this.chatHasta = this.ahora() + espera * 1000;
    }
  }
  esperaChat() { return Math.max(0, Math.ceil((this.chatHasta - this.ahora()) / 1000)); }
  esperaVoz() { return Math.max(0, Math.ceil((this.vozHasta - this.ahora()) / 1000)); }
  esperaCartesia() { return Math.max(0, Math.ceil((this.cartesiaHasta - this.ahora()) / 1000)); }
  bloqueada() { return this.agotada || this.credenciales || this.esperaChat() > 0; }
}
