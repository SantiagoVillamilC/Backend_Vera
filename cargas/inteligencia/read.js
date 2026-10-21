// Perfil de lectura de la prueba de carga de M8. Modelo abierto: constant-arrival-rate, nunca VUs.
// Rotación de usuario y de X-Real-IP por petición para medir el servicio y no el limitador.
import http from 'k6/http'
import { check } from 'k6'

const tokens = JSON.parse(open(__ENV.TOKENS || '/out/tokens.json'))
const base = __ENV.BASE || 'http://127.0.0.1:8080'
const mes = __ENV.MES || '2026-10'

export const options = {
  scenarios: {
    lectura: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 40),
      timeUnit: '1s',
      duration: __ENV.DURATION || '120s',
      preAllocatedVUs: Number(__ENV.PRE_VUS || 100),
      maxVUs: Number(__ENV.MAX_VUS || 500),
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
    dropped_iterations: ['rate<0.01'],
    checks: ['rate>0.99'],
  },
}

function ipDelIteracion() {
  const tercero = (__VU * 13 + __ITER) % 250
  const cuarto = ((__VU * 29 + __ITER) % 250) + 1
  return `10.20.${tercero}.${cuarto}`
}

function rutaDelIteracion() {
  const sorteo = __ITER % 20
  if (sorteo < 12) {
    return `/v1/inteligencia/movimientos?limit=20`
  }
  if (sorteo < 17) {
    return `/v1/inteligencia/resumen?mes=${mes}`
  }
  return `/v1/inteligencia/avisos?mes=${mes}`
}

export default function () {
  const indice = (__VU * 7919 + __ITER) % tokens.length
  const token = tokens[indice]
  const res = http.get(`${base}${rutaDelIteracion()}`, {
    headers: {
      Authorization: `Bearer ${token.token}`,
      'X-Real-IP': ipDelIteracion(),
    },
    tags: { usuario: 'sintetico' },
  })
  check(res, {
    'status 200': (r) => r.status === 200,
    'sin 401': (r) => r.status !== 401,
    'sin 429': (r) => r.status !== 429,
  })
}

export function handleSummary(data) {
  const run = __ENV.RUN || 'local'
  return {
    stdout: JSON.stringify(
      {
        escenario: 'lectura',
        http_reqs: data.metrics.http_reqs.values.count,
        duracion: data.metrics.http_req_duration.values,
        fallidos: data.metrics.http_req_failed.values.rate,
        omisiones: data.metrics.dropped_iterations ? data.metrics.dropped_iterations.values.rate : 0,
        checks: data.metrics.checks.values.rate,
      },
      null,
      2
    ),
    [`/out/k6-lectura-${run}.json`]: JSON.stringify(data),
  }
}
