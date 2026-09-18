package nucleo

import (
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
)

// HU-NF-08, criterios 8 y 22: la tabla cerrada de libro contra extracto, sin margen de criterio.
func TestClasificarParDeEstadosCubreLaTablaCerrada(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre          string
		estadoLibro     string
		estadoExtracto  string
		quiereDesenlace bool
		tipo            string
		adoptar         bool
		compensar       bool
	}{
		{"criterio 8.1: despachada que el extracto confirma adopta el extracto", EstadoTransferenciaDespachada, estadoExtractoConfirmada, true, TipoDescuadreEstadoDistinto, true, false},
		{"criterio 8.2: indeterminada que el extracto rechaza adopta el extracto y compensa", EstadoTransferenciaIndeterminada, estadoExtractoRechazada, true, TipoDescuadreEstadoDistinto, true, true},
		{"criterio 8.2: despachada que el extracto rechaza adopta el extracto y compensa", EstadoTransferenciaDespachada, estadoExtractoRechazada, true, TipoDescuadreEstadoDistinto, true, true},
		{"criterio 22: confirmada que el extracto contradice se escala", EstadoTransferenciaConfirmada, estadoExtractoRechazada, true, TipoDescuadreEstadoFinalContradicho, false, false},
		{"criterio 22: compensada que el extracto contradice se escala", EstadoTransferenciaCompensada, estadoExtractoConfirmada, true, TipoDescuadreEstadoFinalContradicho, false, false},
		{"sin diferencia: confirmada con el extracto confirmada", EstadoTransferenciaConfirmada, estadoExtractoConfirmada, false, "", false, false},
		{"sin diferencia: compensada con el extracto rechazada", EstadoTransferenciaCompensada, estadoExtractoRechazada, false, "", false, false},
		{"sin clasificación: un estado que no se empareja", EstadoTransferenciaCreada, estadoExtractoConfirmada, false, "", false, false},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()

			desenlace, hay := clasificarParDeEstados(caso.estadoLibro, caso.estadoExtracto)
			if hay != caso.quiereDesenlace {
				t.Fatalf("hay desenlace = %v, quiero %v", hay, caso.quiereDesenlace)
			}
			if !hay {
				return
			}
			if desenlace.tipo != caso.tipo {
				t.Errorf("tipo = %q, quiero %q", desenlace.tipo, caso.tipo)
			}
			if desenlace.compensar != caso.compensar {
				t.Errorf("compensar = %v, quiero %v", desenlace.compensar, caso.compensar)
			}
			if desenlace.adoptar != caso.adoptar {
				t.Errorf("adoptar = %v, quiero %v", desenlace.adoptar, caso.adoptar)
			}
		})
	}
}

// HU-NF-08, criterio 8: el compare-and-set va por los estados válidos de la máquina.
func TestTransicionesDeCierrePasanPorLosEstadosValidos(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre       string
		estadoActual string
		nuevo        string
		quiero       []transicionCierre
	}{
		{
			"despachada a confirmada en un solo paso",
			EstadoTransferenciaDespachada, EstadoTransferenciaConfirmada,
			[]transicionCierre{{esperado: EstadoTransferenciaDespachada, nuevo: EstadoTransferenciaConfirmada}},
		},
		{
			"indeterminada a confirmada en un solo paso",
			EstadoTransferenciaIndeterminada, EstadoTransferenciaConfirmada,
			[]transicionCierre{{esperado: EstadoTransferenciaIndeterminada, nuevo: EstadoTransferenciaConfirmada}},
		},
		{
			"despachada a compensada pasa por fallida",
			EstadoTransferenciaDespachada, EstadoTransferenciaCompensada,
			[]transicionCierre{
				{esperado: EstadoTransferenciaDespachada, nuevo: EstadoTransferenciaFallida},
				{esperado: EstadoTransferenciaFallida, nuevo: EstadoTransferenciaCompensada},
			},
		},
		{
			"indeterminada a compensada en un solo paso",
			EstadoTransferenciaIndeterminada, EstadoTransferenciaCompensada,
			[]transicionCierre{{esperado: EstadoTransferenciaIndeterminada, nuevo: EstadoTransferenciaCompensada}},
		},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()

			transiciones := transicionesDeCierre(caso.estadoActual, caso.nuevo)
			if len(transiciones) != len(caso.quiero) {
				t.Fatalf("transiciones = %d, quiero %d", len(transiciones), len(caso.quiero))
			}
			for i, transicion := range transiciones {
				if transicion != caso.quiero[i] {
					t.Errorf("transición %d = %+v, quiero %+v", i, transicion, caso.quiero[i])
				}
			}
		})
	}
}

// HU-NF-08, criterio 12: la escalada identifica la diferencia con su tipo, identidad y montos.
func TestCamposDelDescuadreIdentificanLaDiferencia(t *testing.T) {
	t.Parallel()

	fila := repositorio.Descuadre{
		ID:                7,
		Tipo:              TipoDescuadreMontoDistinto,
		MontoInterno:      pgtype.Int8{Int64: 100000, Valid: true},
		MontoExterno:      pgtype.Int8{Int64: 99999, Valid: true},
		ReferenciaExterna: pgtype.Text{},
	}
	fila.TransferenciaID = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}

	campos := camposDelDescuadre(fila)
	porClave := make(map[string]string, len(campos))
	for _, campo := range campos {
		porClave[campo.Clave] = campo.Valor
	}

	if porClave["descuadre_id"] != "7" || porClave["tipo"] != TipoDescuadreMontoDistinto {
		t.Errorf("campos = %v, quiero el id y el tipo", porClave)
	}
	if porClave["monto_interno"] != "100000" || porClave["monto_externo"] != "99999" {
		t.Errorf("campos = %v, quiero los dos montos en centavos", porClave)
	}
	if _, hay := porClave["transferencia_id"]; !hay {
		t.Errorf("campos = %v, quiero la transferencia cuando existe", porClave)
	}
	if _, hay := porClave["referencia_externa"]; hay {
		t.Errorf("campos = %v, no quiero la referencia externa cuando no existe", porClave)
	}
}
