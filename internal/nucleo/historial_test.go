package nucleo

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const usuarioDelHistorial = "aaaaaaaa-0000-4000-8000-000000000001"

func apunteEn(entryID int64, tipo, cuenta string, monto int64) apunte {
	return apunte{entryID: entryID, tipo: tipo, cuentaID: cuenta, monto: monto}
}

// HU-NF-05 Criterios 4, 5, 9 y 12: cada línea del usuario con su signo y su motivo, sin combinarlas.
func TestDerivarLineas(t *testing.T) {
	for name, tc := range casosDeDerivarLineas() {
		t.Run(name, func(t *testing.T) {
			got, err := derivarLineas(usuarioDelHistorial, tc.apuntes)
			if err != nil {
				t.Fatalf("derivarLineas: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("lineas = %+v, quiero %+v", got, tc.want)
			}
		})
	}
}

type casoDeLineas struct {
	apuntes []apunte
	want    []LineaMovimiento
}

func casosDeDerivarLineas() map[string]casoDeLineas {
	casos := casosDeEntradasYEnvios()
	for name, caso := range casosDeTransferenciasYCompensaciones() {
		casos[name] = caso
	}
	return casos
}

func casosDeEntradasYEnvios() map[string]casoDeLineas {
	disponible := NombreCuentaDisponible(usuarioDelHistorial)
	retenido := NombreCuentaRetenido(usuarioDelHistorial)
	return map[string]casoDeLineas{
		"un deposito entra positivo": {
			apuntes: []apunte{apunteEn(1, "deposito", disponible, -1000000)},
			want:    []LineaMovimiento{{Monto: 1000000, Motivo: MotivoDeposito}},
		},
		"un envio sin gmf es una sola linea": {
			apuntes: []apunte{
				apunteEn(2, "retencion", disponible, 500000),
				apunteEn(2, "retencion", retenido, -500000),
			},
			want: []LineaMovimiento{{Monto: -500000, Motivo: MotivoEnvio}},
		},
		"un envio con gmf muestra monto y gravamen": {
			apuntes: []apunte{
				apunteEn(3, "retencion", disponible, 500000),
				apunteEn(3, "retencion", disponible, 2000),
				apunteEn(3, "retencion", retenido, -500000),
				apunteEn(3, "retencion", CuentaGMFPorPagar, -2000),
			},
			want: []LineaMovimiento{
				{Monto: -500000, Motivo: MotivoEnvio},
				{Monto: -2000, Motivo: MotivoGravamen},
			},
		},
		"el receptor ve su transferencia recibida": {
			apuntes: []apunte{
				apunteEn(4, "transferencia_interna", retenido, 500000),
				apunteEn(4, "transferencia_interna", disponible, -500000),
			},
			want: []LineaMovimiento{{Monto: 500000, Motivo: MotivoTransferenciaRecibida}},
		},
	}
}

func casosDeTransferenciasYCompensaciones() map[string]casoDeLineas {
	disponible := NombreCuentaDisponible(usuarioDelHistorial)
	retenido := NombreCuentaRetenido(usuarioDelHistorial)
	return map[string]casoDeLineas{
		"una compensacion con gmf no es un cero": {
			apuntes: []apunte{
				apunteEn(5, "compensacion", disponible, -500000),
				apunteEn(5, "compensacion", disponible, -2000),
				apunteEn(5, "compensacion", CuentaLiquidacionPendienteRiel, 500000),
				apunteEn(5, "compensacion", CuentaGMFPorPagar, 2000),
			},
			want: []LineaMovimiento{
				{Monto: 500000, Motivo: MotivoDevolucion},
				{Monto: 2000, Motivo: MotivoDevolucionGravamen},
			},
		},
		"el despacho al riel no agrega linea propia": {
			apuntes: []apunte{
				apunteEn(6, "retencion", disponible, 500000),
				apunteEn(6, "retencion", retenido, -500000),
				apunteEn(7, "despacho_riel", retenido, 500000),
				apunteEn(7, "despacho_riel", CuentaLiquidacionPendienteRiel, -500000),
			},
			want: []LineaMovimiento{{Monto: -500000, Motivo: MotivoEnvio}},
		},
	}
}

// HU-NF-05 Criterio 4.6: el motivo sale de un conjunto cerrado; lo inclasificable es un error.
func TestDerivarLineasFallaSiElMotivoNoSePuedeDerivar(t *testing.T) {
	disponible := NombreCuentaDisponible(usuarioDelHistorial)
	retenido := NombreCuentaRetenido(usuarioDelHistorial)

	tests := map[string][]apunte{
		"retencion sin contraparte": {apunteEn(1, "retencion", disponible, 500000)},
		"tipo desconocido":          {apunteEn(2, "asiento_nuevo", disponible, 500000), apunteEn(2, "asiento_nuevo", retenido, -500000)},
	}

	for name, apuntes := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := derivarLineas(usuarioDelHistorial, apuntes); err == nil {
				t.Fatal("derivarLineas no falló, quiero un motivo inclasificable rechazado")
			}
		})
	}
}

// HU-NF-05 Criterio 4.2: las líneas que no son de la subcuenta disponible no se muestran.
func TestDerivarLineasSoloMuestraLaSubcuentaDisponible(t *testing.T) {
	disponible := NombreCuentaDisponible(usuarioDelHistorial)
	otro := NombreCuentaDisponible("bbbbbbbb-0000-4000-8000-000000000002")

	apuntes := []apunte{
		apunteEn(1, "retencion", disponible, 500000),
		apunteEn(1, "retencion", NombreCuentaRetenido(usuarioDelHistorial), -500000),
		apunteEn(1, "transferencia_interna", NombreCuentaRetenido(usuarioDelHistorial), 500000),
		apunteEn(1, "transferencia_interna", otro, -500000),
	}

	got, err := derivarLineas(usuarioDelHistorial, apuntes)
	if err != nil {
		t.Fatalf("derivarLineas: %v", err)
	}
	want := []LineaMovimiento{{Monto: -500000, Motivo: MotivoEnvio}}
	if !slices.Equal(got, want) {
		t.Errorf("lineas = %+v, quiero solo %+v (ni el retenido ni la cuenta ajena)", got, want)
	}
}

// HU-NF-05 Criterio 4.6: el conjunto de motivos es cerrado y son los seis de la unidad.
func TestMotivosDelHistorialSonSeisValoresCerrados(t *testing.T) {
	got := []MotivoMovimiento{
		MotivoDeposito,
		MotivoTransferenciaRecibida,
		MotivoEnvio,
		MotivoGravamen,
		MotivoDevolucion,
		MotivoDevolucionGravamen,
	}
	want := []string{
		"deposito",
		"transferencia_recibida",
		"envio",
		"gravamen",
		"devolucion",
		"devolucion_gravamen",
	}
	if len(got) != 6 {
		t.Fatalf("motivos = %d, quiero 6", len(got))
	}
	for i, motivo := range got {
		if string(motivo) != want[i] {
			t.Errorf("motivo[%d] = %q, quiero %q", i, motivo, want[i])
		}
	}
}

// HU-NF-05 Criterio 4.6: los tipos de asiento son el conjunto cerrado de seis del diseño.
func TestTiposDeAsientoSonLosSeisDelDiseno(t *testing.T) {
	got := []string{
		TipoAsientoDeposito,
		TipoAsientoRetencion,
		TipoAsientoTransferenciaInterna,
		TipoAsientoDespachoRiel,
		TipoAsientoLiquidacionConfirmada,
		TipoAsientoCompensacion,
	}
	want := []string{
		"deposito",
		"retencion",
		"transferencia_interna",
		"despacho_riel",
		"liquidacion_confirmada",
		"compensacion",
	}
	if !slices.Equal(got, want) {
		t.Errorf("tipos = %v, quiero %v", got, want)
	}
}

// HU-NF-05 Criterios 6 y 7: el monto viaja como cadena exacta, nunca como número JSON.
func TestLineaMovimientoSerializaElMontoComoCadena(t *testing.T) {
	b, err := json.Marshal(LineaMovimiento{Monto: Centavos(9007199254740993), Motivo: MotivoEnvio})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(b), `{"monto":"9007199254740993","motivo":"envio"}`; got != want {
		t.Errorf("json = %s, quiero %s", got, want)
	}
}

// HU-NF-05: un límite de página negativo o que desborda el int32 de la consulta se rechaza explícito, nunca se convierte en silencio.
func TestConsultarPaginaRechazaLimiteInvalido(t *testing.T) {
	tests := map[string]int{
		"limite negativo":           -1,
		"limite que desborda int32": math.MaxInt32,
	}
	for name, limite := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := consultarPagina(context.Background(), nil, usuarioDelHistorial, nil, nil, limite)
			if !errors.Is(err, ErrLimiteDePaginaInvalido) {
				t.Fatalf("consultarPagina(%d) error = %v, quiero ErrLimiteDePaginaInvalido", limite, err)
			}
		})
	}
}

// HU-NF-05 Criterio 9: la clave del grupo es la transferencia o, cuando no hay, el propio asiento.
func TestGrupoDeFila(t *testing.T) {
	transferencia := uuid.New()
	conTransferencia := repositorio.LineasDeMovimientosRow{
		EntryID:         9,
		TransferenciaID: pgtype.UUID{Bytes: transferencia, Valid: true},
	}
	if got := grupoDeFila(conTransferencia); got != transferencia.String() {
		t.Errorf("grupo = %q, quiero %q", got, transferencia.String())
	}

	sinTransferencia := repositorio.LineasDeMovimientosRow{EntryID: 9}
	if got := grupoDeFila(sinTransferencia); got != "entry:9" {
		t.Errorf("grupo = %q, quiero %q", got, "entry:9")
	}
}
