//go:build integration

package nucleo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// directorioEstatico resuelve cualquier llave válida a la misma entidad y producto, sin estado compartido.
type directorioEstatico struct{}

func (directorioEstatico) Resolver(context.Context, Llave) (ResolucionDirectorio, error) {
	return ResolucionDirectorio{Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"}, nil
}

// servicioDeLlave arma el servicio del envío por llave con el nivel de KYC único y el directorio dado.
func servicioDeLlave(pool *pgxpool.Pool, directorio DirectorioLlaves, riel Riel) *Servicio {
	return NewServicio(pool, LectorNivelKYCFijo{}, riel, nil, nil, ConDirectorio(directorio))
}

// solicitudDeLlave arma la petición del envío por llave con una clave nueva.
func solicitudDeLlave(usuario, cuenta, llave string, monto Centavos) SolicitudEnvioPorLlave {
	return SolicitudEnvioPorLlave{
		UsuarioID:      usuario,
		CuentaOrigen:   cuenta,
		TipoLlave:      TipoLlaveCelular,
		Llave:          llave,
		Monto:          monto,
		Moneda:         MonedaCOP,
		IdempotencyKey: "k-" + uuid.NewString(),
	}
}

// errFallaOutbox marca la línea del INSERT del outbox que la prueba obliga a fallar.
var errFallaOutbox = errors.New("prueba: falla inyectada en la fila de outbox")

// abridorConFallaOutbox envuelve el pool y devuelve transacciones cuyo INSERT de outbox falla.
type abridorConFallaOutbox struct{ real abridorDeTransacciones }

func (a abridorConFallaOutbox) Begin(ctx context.Context) (transaccionDeNucleo, error) {
	tx, err := a.real.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return transaccionConFallaOutbox{transaccionDeNucleo: tx}, nil
}

// transaccionConFallaOutbox inyecta el error solo en el INSERT de la fila de outbox.
type transaccionConFallaOutbox struct{ transaccionDeNucleo }

func (t transaccionConFallaOutbox) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "INSERT INTO nucleo.outbox") {
		return filaSinResultado{err: errFallaOutbox}
	}
	return t.transaccionDeNucleo.QueryRow(ctx, sql, args...)
}

// HU-NF-10, criterio 10: la petición válida reserva con retención y outbox, sin llamar al riel ni mover el dinero en vuelo.
func TestHU_NF_10_Criterio10_EnvioPorLlaveReservaConOutboxSinLlamarAlRiel(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	riel := &rielDoble{}
	s := servicioDeLlave(pool, directorioEstatico{}, riel)
	liquidacionAntes := saldoDeCuentaDePrueba(t, base, CuentaLiquidacionPendienteRiel)
	transferencia, creada, err := s.TransferirPorLlave(ctx, solicitudDeLlave(usuario, cuenta, "3001234567", 250000))
	if err != nil || !creada {
		t.Fatalf("envío: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	if transferencia.Estado != EstadoTransferenciaReservada {
		t.Errorf("estado = %q, quiero %q", transferencia.Estado, EstadoTransferenciaReservada)
	}
	if llamadas := riel.Llamadas(); llamadas != 0 {
		t.Errorf("llamadas al riel durante la petición = %d, quiero 0", llamadas)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferencia.ID, TipoAsientoRetencion); asientos != 1 {
		t.Errorf("asientos de retención = %d, quiero 1", asientos)
	}
	if filas := contarOutboxDeTransferencia(t, ctx, pool, transferencia.ID); filas != 1 {
		t.Errorf("filas de outbox = %d, quiero 1", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 750000 {
		t.Errorf("disponible del emisor = %d, quiero 750000", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaRetenido(usuario)); got != 250000 {
		t.Errorf("retenido del emisor = %d, quiero 250000", got)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferencia.ID, TipoAsientoDespachoRiel); asientos != 0 {
		t.Errorf("asientos de despacho durante la petición = %d, quiero 0", asientos)
	}
	if got := saldoDeCuentaDePrueba(t, base, CuentaLiquidacionPendienteRiel); got != liquidacionAntes {
		t.Errorf("liquidación pendiente durante la petición = %d, quiero %d", got, liquidacionAntes)
	}
	verificarApendiceC(t, pool, transferencia.ID, []string{cuenta, NombreCuentaRetenido(usuario)})

	if err := s.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}
	if llamadas := riel.Llamadas(); llamadas != 1 {
		t.Errorf("llamadas al riel tras el worker = %d, quiero 1", llamadas)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferencia.ID); estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado tras el worker = %q, quiero %q", estado, EstadoTransferenciaConfirmada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferencia.ID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos de despacho tras el worker = %d, quiero 1", asientos)
	}
	if got := saldoDeCuentaDePrueba(t, base, CuentaLiquidacionPendienteRiel); got != liquidacionAntes {
		t.Errorf("liquidación pendiente tras confirmar = %d, quiero %d", got, liquidacionAntes)
	}
	t.Logf("la petición reservó con outbox sin tocar el riel; el worker confirmó y cerró la transferencia")
}

// HU-NF-10, criterio 10.1: si la fila de outbox falla, no queda ni transferencia ni retención.
func TestHU_NF_10_Criterio10_1_FallaDelOutboxRevierteTodaLaReserva(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	asientosAntes, postingAntes := contarAsientosYPosting(t, pool)

	s := servicioDeLlave(pool, directorioEstatico{}, nil)
	s.pool = abridorConFallaOutbox{real: s.pool}
	_, _, err := s.TransferirPorLlave(ctx, solicitudDeLlave(usuario, cuenta, "3001234567", 250000))
	if !errors.Is(err, errFallaOutbox) {
		t.Fatalf("err = %v, quiero la falla inyectada del outbox", err)
	}

	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 1000000 {
		t.Errorf("disponible del emisor = %d, quiero 1000000 intacto", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaRetenido(usuario)); got != 0 {
		t.Errorf("retenido del emisor = %d, quiero 0", got)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
	asientosDespues, postingDespues := contarAsientosYPosting(t, pool)
	if asientosAntes != asientosDespues || postingAntes != postingDespues {
		t.Errorf("el diario cambió: journal_entry %d->%d, posting %d->%d", asientosAntes, asientosDespues, postingAntes, postingDespues)
	}
	t.Logf("la falla del outbox revirtió la transferencia y la retención juntas")
}

// HU-NF-10, criterio 7: el reintento devuelve el mismo id y estado, y deja una sola transferencia y una sola fila de outbox.
func TestHU_NF_10_Criterio7_ReintentoNoDuplicaElEnvio(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	s := servicioDeLlave(pool, directorioEstatico{}, nil)
	solicitud := solicitudDeLlave(usuario, cuenta, "3001234567", 250000)

	primera, creada, err := s.TransferirPorLlave(ctx, solicitud)
	if err != nil || !creada {
		t.Fatalf("primer envío: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	segunda, creada, err := s.TransferirPorLlave(ctx, solicitud)
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if creada {
		t.Error("el reintento no debe crear una segunda transferencia")
	}
	if segunda.ID != primera.ID || segunda.Estado != primera.Estado {
		t.Errorf("reintento = %s/%q, quiero %s/%q", segunda.ID, segunda.Estado, primera.ID, primera.Estado)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("transferencias del emisor = %d, quiero 1", filas)
	}
	if filas := contarOutboxDeTransferencia(t, ctx, pool, primera.ID); filas != 1 {
		t.Errorf("filas de outbox = %d, quiero 1", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 750000 {
		t.Errorf("disponible del emisor = %d, quiero 750000 sin doble débito", got)
	}
	t.Logf("el reintento devolvió %s en %q con una sola fila de transferencia y de outbox", primera.ID, primera.Estado)
}

// HU-NF-10, criterio 7: la misma clave con otro cuerpo se rechaza sin crear un segundo envío.
func TestHU_NF_10_Criterio7_ReintentoConOtroCuerpoEsConflicto(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	s := servicioDeLlave(pool, directorioEstatico{}, nil)
	solicitud := solicitudDeLlave(usuario, cuenta, "3001234567", 250000)
	if _, creada, err := s.TransferirPorLlave(ctx, solicitud); err != nil || !creada {
		t.Fatalf("primer envío: creada = %v, err = %v", creada, err)
	}

	otro := solicitud
	otro.Monto = 200000
	if _, _, err := s.TransferirPorLlave(ctx, otro); !errors.Is(err, ErrIdempotenciaEnConflicto) {
		t.Fatalf("otro monto con la misma clave: err = %v, quiero ErrIdempotenciaEnConflicto", err)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("transferencias del emisor = %d, quiero 1", filas)
	}
}

// HU-NF-10, criterio 7: dos reintentos en paralelo con la misma clave dejan una sola transferencia.
func TestHU_NF_10_Criterio7_ReintentosParalelosNoDuplican(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	s := servicioDeLlave(pool, directorioEstatico{}, nil)
	solicitud := solicitudDeLlave(usuario, cuenta, "3001234567", 250000)

	type resultado struct {
		transferencia Transferencia
		creada        bool
		err           error
	}
	resultados := make([]resultado, 2)
	var wg sync.WaitGroup
	for i := range resultados {
		wg.Go(func() {
			resultados[i].transferencia, resultados[i].creada, resultados[i].err = s.TransferirPorLlave(ctx, solicitud)
		})
	}
	wg.Wait()

	creadas := 0
	for i, r := range resultados {
		if r.err != nil {
			t.Fatalf("reintento %d: %v", i, r.err)
		}
		if r.creada {
			creadas++
		}
	}
	if creadas != 1 {
		t.Errorf("creadas = %d, quiero exactamente 1", creadas)
	}
	if resultados[0].transferencia.ID != resultados[1].transferencia.ID {
		t.Errorf("ids distintos: %s y %s", resultados[0].transferencia.ID, resultados[1].transferencia.ID)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("transferencias del emisor = %d, quiero 1", filas)
	}
	if filas := contarOutboxDeTransferencia(t, ctx, pool, resultados[0].transferencia.ID); filas != 1 {
		t.Errorf("filas de outbox = %d, quiero 1", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 750000 {
		t.Errorf("disponible del emisor = %d, quiero 750000 sin doble débito", got)
	}
}

// HU-NF-10, criterios 1 y 3: una llave de tipo desconocido o un monto no positivo no dejan rastro.
func TestHU_NF_10_Criterios1y3_RechazosNoRegistranNada(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	s := servicioDeLlave(pool, directorioEstatico{}, nil)
	casos := map[string]struct {
		ajuste   func(*SolicitudEnvioPorLlave)
		esperado error
	}{
		"monto cero":        {ajuste: func(s *SolicitudEnvioPorLlave) { s.Monto = 0 }, esperado: ErrMontoInvalido},
		"monto negativo":    {ajuste: func(s *SolicitudEnvioPorLlave) { s.Monto = -1 }, esperado: ErrMontoInvalido},
		"tipo desconocido":  {ajuste: func(s *SolicitudEnvioPorLlave) { s.TipoLlave = "nip" }, esperado: ErrLlaveInvalida},
		"forma que no toca": {ajuste: func(s *SolicitudEnvioPorLlave) { s.Llave = "2001234567" }, esperado: ErrLlaveInvalida},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			solicitud := solicitudDeLlave(usuario, cuenta, "3001234567", 1000)
			caso.ajuste(&solicitud)
			if _, _, err := s.TransferirPorLlave(ctx, solicitud); !errors.Is(err, caso.esperado) {
				t.Fatalf("err = %v, quiero %v", err, caso.esperado)
			}
		})
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 1000000 {
		t.Errorf("disponible del emisor = %d, quiero 1000000 intacto", got)
	}
}

// HU-NF-10, criterios 4 y 5: un directorio caído falla sin dejar transferencia ni saldo retenido.
func TestHU_NF_10_Criterios4y5_DirectorioCaidoNoDejaReserva(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	caido := &directorioFalso{fn: func(context.Context, Llave) (ResolucionDirectorio, error) {
		return ResolucionDirectorio{}, errors.New("conexión rechazada")
	}}
	s := servicioDeLlave(pool, caido, nil)
	if _, _, err := s.TransferirPorLlave(ctx, solicitudDeLlave(usuario, cuenta, "3001234567", 250000)); !errors.Is(err, ErrDirectorioSinRespuesta) {
		t.Fatalf("err = %v, quiero ErrDirectorioSinRespuesta", err)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 1000000 {
		t.Errorf("disponible del emisor = %d, quiero 1000000 intacto", got)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaRetenido(usuario)); got != 0 {
		t.Errorf("retenido del emisor = %d, quiero 0", got)
	}
}

// HU-NF-10, criterio 4: mientras el directorio tarda no hay transacción abierta ni saldo bloqueado.
func TestHU_NF_10_Criterio4_DirectorioLentoNoBloqueaLaReserva(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	lento := &directorioQueEspera{entro: make(chan struct{}), seguir: make(chan struct{})}
	s := servicioDeLlave(pool, lento, nil)

	listo := make(chan error, 1)
	go func() {
		_, _, err := s.TransferirPorLlave(context.Background(), solicitudDeLlave(usuario, cuenta, "3001234567", 250000))
		listo <- err
	}()

	<-lento.entro
	if abiertas, locks := contarTransaccionesYBloqueos(t, pool); abiertas != 0 || locks != 0 {
		t.Errorf("con el directorio en vuelo hay %d transacciones y %d locks, quiero 0 y 0", abiertas, locks)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 0 {
		t.Errorf("transferencias durante la espera = %d, quiero 0", filas)
	}
	close(lento.seguir)
	if err := <-listo; err != nil {
		t.Fatalf("envío tras liberar el directorio: %v", err)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("transferencias tras liberar = %d, quiero 1", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, NombreCuentaRetenido(usuario)); got != 250000 {
		t.Errorf("retenido tras liberar = %d, quiero 250000", got)
	}
	t.Logf("la consulta al directorio ocurrió antes de abrir la transacción, sin bloqueos")
}

// HU-NF-10, criterios 8 y 9: la ventana mensual suma los envíos de las dos historias y el GMF se aparta como concepto propio.
func TestHU_NF_10_Criterios8y9_LimitesYGMFCompartidosConElEnvioInterno(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, topeCanonico2026)
	momento := time.Now().UTC()

	s := servicioDeLlave(pool, directorioEstatico{}, nil)

	t.Run("el cupo mensual suma el envío interno del mes", func(t *testing.T) {
		insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeCanonico2026-100000, EstadoTransferenciaReservada, momento)
		if _, _, err := s.TransferirPorLlave(ctx, solicitudDeLlave(usuario, cuenta, "3001234567", 200000)); !errors.Is(err, ErrCupoMensualExcedido) {
			t.Fatalf("clave + interno del mes: err = %v, quiero ErrCupoMensualExcedido", err)
		}
		if _, _, err := s.TransferirPorLlave(ctx, solicitudDeLlave(usuario, cuenta, "3001234567", 50000)); err != nil {
			t.Fatalf("clave que cabe en el residuo: err = %v, quiero nil", err)
		}
	})

	otro := uuid.NewString()
	otraCuenta := subcuentaDisponibleDePrueba(t, ctx, base, otro)
	fondearDisponibleDePrueba(t, pool, otraCuenta, 2000000)
	insertarTransferenciaDePrueba(t, pool, otro, otraCuenta, "intra:"+uuid.NewString(), topeExentoGMF2026, EstadoTransferenciaReservada, momento)

	transferencia, creada, err := s.TransferirPorLlave(ctx, solicitudDeLlave(otro, otraCuenta, "3001234567", 1000000))
	if err != nil || !creada {
		t.Fatalf("envío sobre el tope exento: creada = %v, err = %v", creada, err)
	}
	lineas := lineasDeTransferencia(t, pool, transferencia.ID)
	if len(lineas) != 4 {
		t.Fatalf("líneas de la retención = %d, quiero 4 con el gravamen", len(lineas))
	}
	var gravamen int64
	for _, linea := range lineas {
		if linea.cuenta == CuentaGMFPorPagar {
			gravamen = linea.monto
		}
	}
	if gravamen != -4000 {
		t.Errorf("línea del GMF por pagar = %d, quiero -4000", gravamen)
	}
	t.Logf("el cupo sumó el envío interno del mes y el GMF quedó como concepto propio por 4000 centavos")
}

// HU-NF-10, criterio 6: el saldo que no cubre el monto se rechaza sin registrar la operación.
func TestHU_NF_10_Criterio6_SaldoInsuficienteNoRegistra(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000)

	s := servicioDeLlave(pool, directorioEstatico{}, nil)
	if _, _, err := s.TransferirPorLlave(ctx, solicitudDeLlave(usuario, cuenta, "3001234567", 200000)); !errors.Is(err, ErrSaldoInsuficiente) {
		t.Fatalf("err = %v, quiero ErrSaldoInsuficiente", err)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 0 {
		t.Errorf("transferencias del emisor = %d, quiero 0", filas)
	}
	if got := saldoDeCuentaDePrueba(t, base, cuenta); got != 1000 {
		t.Errorf("disponible del emisor = %d, quiero 1000 intacto", got)
	}
}

// HU-NF-10, criterio 11: el handler responde 201 con el envío en proceso y 200 con el ya guardado para la misma clave.
func TestHU_NF_10_Criterio11_HandlerRespondeElEnvioEnProceso(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	handler := NewHandler(servicioDeLlave(pool, directorioEstatico{}, nil))
	cuerpo := `{"tipo_llave":"celular","llave":"3001234567","monto":"250000","idempotency_key":"k-` + uuid.NewString() + `"}`
	pedir := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/transferencias/interbancarias", strings.NewReader(cuerpo))
		handler.TransferirInterbancaria(rec, r.WithContext(identidad.ConUsuarioID(r.Context(), usuario)))
		return rec
	}

	primera := pedir()
	if primera.Code != http.StatusCreated {
		t.Fatalf("primer envío: status = %d, quiero 201 (cuerpo %q)", primera.Code, primera.Body.String())
	}
	var envelope api.Envelope[RespuestaEnvioPorLlave]
	if err := json.Unmarshal(primera.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decodificar el envelope: %v", err)
	}
	if !envelope.Success || envelope.Data.ID == "" {
		t.Fatalf("envelope = %+v, quiero éxito con id", envelope)
	}
	if envelope.Data.Estado != EstadoTransferenciaReservada {
		t.Errorf("estado = %q, quiero %q: la respuesta no afirma entrega", envelope.Data.Estado, EstadoTransferenciaReservada)
	}

	segunda := pedir()
	if segunda.Code != http.StatusOK {
		t.Fatalf("reintento: status = %d, quiero 200", segunda.Code)
	}
	var repetido api.Envelope[RespuestaEnvioPorLlave]
	if err := json.Unmarshal(segunda.Body.Bytes(), &repetido); err != nil {
		t.Fatalf("decodificar el envelope del reintento: %v", err)
	}
	if repetido.Data.ID != envelope.Data.ID {
		t.Errorf("id del reintento = %q, quiero el original %q", repetido.Data.ID, envelope.Data.ID)
	}
	if filas := contarTransferenciasDeUsuario(t, pool, usuario); filas != 1 {
		t.Errorf("transferencias del emisor = %d, quiero 1", filas)
	}
	t.Logf("el handler respondió 201 con estado reservada y 200 con el mismo id en el reintento")
}

// HU-NF-10, criterio 12.2: la orden entregada al riel lleva la llave, la entidad y el producto resueltos, el monto, la moneda, la idempotencia y el id.
func TestHU_NF_10_Criterio12_2_LaOrdenLlevaEntidadYProducto(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, base, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, 1000000)

	riel := &rielDoble{}
	s := servicioDeLlave(pool, directorioEstatico{}, riel)
	solicitud := solicitudDeLlave(usuario, cuenta, "3001234567", 250000)

	transferencia, creada, err := s.TransferirPorLlave(ctx, solicitud)
	if err != nil || !creada {
		t.Fatalf("envío: creada = %v, err = %v; quiero true, nil", creada, err)
	}
	if err := s.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}
	orden := riel.UltimaOrden()
	if orden.TransferenciaID != transferencia.ID {
		t.Errorf("transferencia_id de la orden = %s, quiero %s", orden.TransferenciaID, transferencia.ID)
	}
	if orden.IdempotencyKey != solicitud.IdempotencyKey {
		t.Errorf("idempotency_key de la orden = %q, quiero %q", orden.IdempotencyKey, solicitud.IdempotencyKey)
	}
	if orden.Llave != solicitud.Llave {
		t.Errorf("llave de la orden = %q, quiero %q", orden.Llave, solicitud.Llave)
	}
	if orden.Entidad != "Banco Vecino" || orden.Producto != "Cuenta de ahorros" {
		t.Errorf("entidad/producto de la orden = %q/%q, quiero Banco Vecino/Cuenta de ahorros", orden.Entidad, orden.Producto)
	}
	if orden.Monto != solicitud.Monto {
		t.Errorf("monto de la orden = %d, quiero %d", orden.Monto, solicitud.Monto)
	}
	if orden.Moneda != solicitud.Moneda {
		t.Errorf("moneda de la orden = %q, quiero %q", orden.Moneda, solicitud.Moneda)
	}
	t.Logf("la orden entregada al riel llevó la llave, la entidad y el producto resueltos, con el mismo id y la misma idempotencia")
}
