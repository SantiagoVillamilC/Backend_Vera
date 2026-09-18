package nucleo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// mapaDeLineas indexa las líneas por cuenta para compararlas sin depender del orden.
func mapaDeLineas(t *testing.T, lineas []repositorio.InsertarLineaParams) map[string]int64 {
	t.Helper()

	porCuenta := make(map[string]int64, len(lineas))
	for _, linea := range lineas {
		if _, repetida := porCuenta[linea.CuentaID]; repetida {
			t.Fatalf("cuenta repetida en el asiento: %q", linea.CuentaID)
		}
		porCuenta[linea.CuentaID] = linea.Monto
	}
	return porCuenta
}

// sumaDeLineas comprueba la partida doble sobre las líneas del asiento.
func sumaDeLineas(t *testing.T, lineas []repositorio.InsertarLineaParams) Centavos {
	t.Helper()

	var suma Centavos
	for _, linea := range lineas {
		nueva, err := suma.Sumar(Centavos(linea.Monto))
		if err != nil {
			t.Fatalf("sumar la línea de %q: %v", linea.CuentaID, err)
		}
		suma = nueva
	}
	return suma
}

// 4a del Apéndice B: liquidación pendiente contra el custodio, suma cero.
func TestLineasDeConfirmacionCuadranContraElCustodio(t *testing.T) {
	t.Parallel()

	transferencia := repositorio.Transferencia{
		ID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		UsuarioID: uuid.NewString(),
		Monto:     1000000,
		Moneda:    MonedaCOP,
	}

	lineas, err := lineasDeConfirmacion(transferencia)
	if err != nil {
		t.Fatalf("lineasDeConfirmacion: %v", err)
	}
	porCuenta := mapaDeLineas(t, lineas)
	if got := porCuenta[CuentaLiquidacionPendienteRiel]; got != 1000000 {
		t.Errorf("liquidación pendiente = %d, quiero 1000000", got)
	}
	if got := porCuenta[CuentaCustodiaBanco]; got != -1000000 {
		t.Errorf("custodia = %d, quiero -1000000", got)
	}
	if suma := sumaDeLineas(t, lineas); suma != 0 {
		t.Errorf("suma del asiento 4a = %d, quiero 0", suma)
	}
}

// 4b del Apéndice B: liquidación pendiente contra el disponible del emisor, suma cero.
func TestLineasDeCompensacionCuadranContraElEmisor(t *testing.T) {
	t.Parallel()

	usuario := uuid.NewString()
	transferencia := repositorio.Transferencia{
		ID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		UsuarioID: usuario,
		Monto:     1000000,
		Moneda:    MonedaCOP,
	}

	lineas, err := lineasDeCompensacion(transferencia)
	if err != nil {
		t.Fatalf("lineasDeCompensacion: %v", err)
	}
	porCuenta := mapaDeLineas(t, lineas)
	if got := porCuenta[CuentaLiquidacionPendienteRiel]; got != 1000000 {
		t.Errorf("liquidación pendiente = %d, quiero 1000000", got)
	}
	if got := porCuenta[NombreCuentaDisponible(usuario)]; got != -1000000 {
		t.Errorf("disponible del emisor = %d, quiero -1000000", got)
	}
	if suma := sumaDeLineas(t, lineas); suma != 0 {
		t.Errorf("suma del asiento 4b = %d, quiero 0", suma)
	}
}

// motivoDeRechazo deja la descripción nula solo cuando el riel no manda motivo.
func TestMotivoDeRechazo(t *testing.T) {
	t.Parallel()

	if v := motivoDeRechazo(""); v.Valid {
		t.Errorf("sin motivo la descripción = %+v, quiero nula", v)
	}
	if v := motivoDeRechazo("fondos_insuficientes"); !v.Valid || v.String != "fondos_insuficientes" {
		t.Errorf("con motivo la descripción = %+v, quiero fondos_insuficientes", v)
	}
}

// rielDeClasificacion es el doble mínimo del riel: siempre devuelve el error configurado.
type rielDeClasificacion struct{ err error }

// Enviar devuelve el error configurado sin tocar la red.
func (r rielDeClasificacion) Enviar(context.Context, OrdenRiel) (RespuestaRiel, error) {
	return RespuestaRiel{}, r.err
}

// ConsultarEstado no lo usa la clasificación del desenlace.
func (r rielDeClasificacion) ConsultarEstado(context.Context, uuid.UUID) (RespuestaRiel, error) {
	return RespuestaRiel{}, ErrRielSinRegistro
}

// txDeClasificacion registra el SQL intentado y responde como una escritura exitosa de una fila.
type txDeClasificacion struct {
	eventos *[]string
}

// Exec registra la sentencia y simula una fila afectada.
func (tx *txDeClasificacion) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	*tx.eventos = append(*tx.eventos, sql)
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

// Query no lo usa el cierre del desenlace.
func (tx *txDeClasificacion) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

// QueryRow registra la sentencia y devuelve una fila escaneable.
func (tx *txDeClasificacion) QueryRow(_ context.Context, sql string, _ ...interface{}) pgx.Row {
	*tx.eventos = append(*tx.eventos, sql)
	return filaConID{id: 1}
}

// Commit cierra la transacción del doble.
func (tx *txDeClasificacion) Commit(context.Context) error { return nil }

// Rollback descarta la transacción del doble.
func (tx *txDeClasificacion) Rollback(context.Context) error { return nil }

// abridorDeClasificacion entrega siempre la misma transacción del doble.
type abridorDeClasificacion struct{ tx transaccionDeNucleo }

// Begin entrega la transacción del doble sin abrir una conexión real.
func (a abridorDeClasificacion) Begin(context.Context) (transaccionDeNucleo, error) {
	return a.tx, nil
}

// despachoDeClasificacion arma una transferencia despachada con su fila de outbox ya reclamada.
func despachoDeClasificacion() despachoReclamado {
	id := uuid.New()
	return despachoReclamado{
		outbox: repositorio.Outbox{ID: 1},
		transferencia: repositorio.Transferencia{
			ID:        pgtype.UUID{Bytes: id, Valid: true},
			UsuarioID: uuid.NewString(),
			Monto:     1000000,
			Moneda:    MonedaCOP,
			Estado:    EstadoTransferenciaDespachada,
		},
		transferenciaID: id,
	}
}

// registraSQL reporta si alguna sentencia registrada contiene el fragmento dado.
func registraSQL(eventos []string, fragmento string) bool {
	for _, sql := range eventos {
		if strings.Contains(sql, fragmento) {
			return true
		}
	}
	return false
}

// M1: el vencimiento propio del riel no se confunde con el vencimiento o la cancelación del padre.
func TestEnviarAlRielDistingueElPlazoDelRielDelPadre(t *testing.T) {
	t.Parallel()

	vencido := fmt.Errorf("riel: %w", context.DeadlineExceeded)

	conPadreVivo := &Servicio{riel: rielDeClasificacion{err: vencido}}
	if _, err := conPadreVivo.enviarAlRiel(t.Context(), repositorio.Transferencia{}); !errors.Is(err, ErrRielSinRespuesta) {
		t.Errorf("con el padre vivo y DeadlineExceeded err = %v, quiero ErrRielSinRespuesta", err)
	}

	ctxVencido, cancelar := context.WithCancel(t.Context())
	cancelar()
	conPadreVencido := &Servicio{riel: rielDeClasificacion{err: vencido}}
	_, err := conPadreVencido.enviarAlRiel(ctxVencido, repositorio.Transferencia{})
	if errors.Is(err, ErrRielSinRespuesta) {
		t.Errorf("con el padre vencido y DeadlineExceeded err = %v, no quiero ErrRielSinRespuesta", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("con el padre vencido el error debe conservarse, err = %v", err)
	}
}

// M1: con el padre vivo el centinela cierra indeterminada sin asiento; con el padre vencido reintenta.
func TestEntregarAlRielClasificaElPlazoDelRielDelPadre(t *testing.T) {
	t.Parallel()

	vencido := fmt.Errorf("riel: %w", context.DeadlineExceeded)

	t.Run("padre vivo cierra indeterminada sin asiento", func(t *testing.T) {
		t.Parallel()

		var eventos []string
		servicio := &Servicio{
			pool: abridorDeClasificacion{tx: &txDeClasificacion{eventos: &eventos}},
			riel: rielDeClasificacion{err: vencido},
		}
		if err := servicio.entregarAlRiel(t.Context(), despachoDeClasificacion()); err != nil {
			t.Fatalf("entregarAlRiel con el padre vivo: %v", err)
		}
		if !registraSQL(eventos, "UPDATE nucleo.transferencia") {
			t.Errorf("no se intentó la transición a indeterminada; sentencias: %v", eventos)
		}
		if registraSQL(eventos, "journal_entry") {
			t.Errorf("el desenlace indeterminado no debe escribir asiento; sentencias: %v", eventos)
		}
	})

	t.Run("padre vencido reintenta sin cerrar", func(t *testing.T) {
		t.Parallel()

		var eventos []string
		servicio := &Servicio{
			pool:              abridorDeClasificacion{tx: &txDeClasificacion{eventos: &eventos}},
			riel:              rielDeClasificacion{err: vencido},
			politicaReintento: PoliticaReintentoOutboxPorDefecto(),
		}
		ctxVencido, cancelar := context.WithCancel(t.Context())
		cancelar()
		err := servicio.entregarAlRiel(ctxVencido, despachoDeClasificacion())
		if err == nil {
			t.Fatalf("con el padre vencido el error debe propagarse por el reintento")
		}
		if errors.Is(err, ErrRielSinRespuesta) {
			t.Errorf("el error del reintento no debe ser el centinela del riel; err = %v", err)
		}
		if registraSQL(eventos, "UPDATE nucleo.transferencia") {
			t.Errorf("el reintento no debe tocar el estado de la transferencia; sentencias: %v", eventos)
		}
		if !registraSQL(eventos, "SET intentos") {
			t.Errorf("no se registró el fallo del outbox para reintentar; sentencias: %v", eventos)
		}
	})
}
