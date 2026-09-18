package nucleo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 65 UVT de 2026 con la UVT de 52.374: 3.404.310 pesos exactos (HU-NF-06, criterio 13).
const topeExentoGMF2026 = Centavos(340431000)

// tarifaCuatroPorMil son los 4000 ppm del 4x1000 del artículo 872 del Estatuto Tributario.
const tarifaCuatroPorMil = int64(4000)

func reglaGMFDePrueba() ReglaLimite {
	return ReglaLimite{Tipo: TipoReglaGrava, Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 6500, TarifaPPM: tarifaCuatroPorMil}
}

// TestHU_NF_06_Criterio13_1_DentroDelTopeExentoNoRetieneGMF cubre que un envío que no cruza las 65 UVT del mes no retiene GMF (HU-NF-06, criterio 13.1).
func TestHU_NF_06_Criterio13_1_DentroDelTopeExentoNoRetieneGMF(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		acumulado Centavos
		monto     Centavos
	}{
		"sin retiros previos y envío bajo el tope":   {acumulado: 0, monto: topeExentoGMF2026 / 2},
		"el envío cae justo en el tope exento":       {acumulado: 0, monto: topeExentoGMF2026},
		"el acumulado y el envío llegan al tope":     {acumulado: 100, monto: topeExentoGMF2026 - 100},
		"el acumulado previo consume todo el tope":   {acumulado: topeExentoGMF2026, monto: 0},
		"acumulado previo menor y envío que no pasa": {acumulado: topeExentoGMF2026 - 1, monto: 1},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			gravamen, err := evaluarGMF([]ReglaLimite{reglaGMFDePrueba()}, uvts2026, caso.acumulado, caso.monto)
			if err != nil {
				t.Fatalf("evaluarGMF: %v, quiero nil", err)
			}
			if gravamen != 0 {
				t.Errorf("gravamen = %d, quiero 0", gravamen)
			}
		})
	}

	lineas, err := lineasDeRetencion(SolicitudRetencion{UsuarioID: uuid.NewString(), Monto: topeExentoGMF2026, Moneda: MonedaCOP})
	if err != nil {
		t.Fatalf("lineasDeRetencion: %v", err)
	}
	if len(lineas) != 2 {
		t.Errorf("líneas de la retención sin GMF = %d, quiero 2", len(lineas))
	}
}

// TestHU_NF_06_Criterio13_2_SoloElExcesoPagaElCuatroPorMil cubre el borde de las 65 UVT y el 4x1000 solo sobre el exceso (HU-NF-06, criterio 13.2).
func TestHU_NF_06_Criterio13_2_SoloElExcesoPagaElCuatroPorMil(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		acumulado Centavos
		monto     Centavos
		querer    Centavos
	}{
		"exactamente 65 UVT no grava":                        {acumulado: 0, monto: topeExentoGMF2026, querer: 0},
		"un centavo de exceso redondea a cero":               {acumulado: 0, monto: topeExentoGMF2026 + 1, querer: 0},
		"124 centavos de exceso redondean a cero":            {acumulado: 0, monto: topeExentoGMF2026 + 124, querer: 0},
		"125 centavos de exceso redondean a un centavo":      {acumulado: 0, monto: topeExentoGMF2026 + 125, querer: 1},
		"el exceso se reparte entre el acumulado y el envío": {acumulado: topeExentoGMF2026, monto: 1000000, querer: 4000},
		"con el tope agotado grava el monto entero":          {acumulado: topeExentoGMF2026 + 1, monto: 1000000, querer: 4000},
		"grava solo el exceso y no el monto entero":          {acumulado: 0, monto: topeExentoGMF2026 + 100000000, querer: 400000},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			gravamen, err := evaluarGMF([]ReglaLimite{reglaGMFDePrueba()}, uvts2026, caso.acumulado, caso.monto)
			if err != nil {
				t.Fatalf("evaluarGMF: %v, quiero nil", err)
			}
			if gravamen != caso.querer {
				t.Errorf("gravamen = %d, quiero %d", gravamen, caso.querer)
			}
		})
	}
}

// TestHU_NF_06_Criterio13_2_SinReglaDeGMFPorVigenteSeRechaza cubre la ausencia de la fila que grava (HU-NF-06, criterio 13).
func TestHU_NF_06_Criterio13_2_SinReglaDeGMFPorVigenteSeRechaza(t *testing.T) {
	t.Parallel()

	reglas := []ReglaLimite{{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 21050}}
	if _, err := evaluarGMF(reglas, uvts2026, 0, topeExentoGMF2026+100000000); !errors.Is(err, ErrReglaGMFNoVigente) {
		t.Fatalf("error = %v, quiero ErrReglaGMFNoVigente", err)
	}
}

// TestHU_NF_06_Criterio13_3_RetencionConGMFEsCuatroLineasConGravamenPropio cubre la forma exacta de las cuatro líneas (HU-NF-06, criterio 13.3).
func TestHU_NF_06_Criterio13_3_RetencionConGMFEsCuatroLineasConGravamenPropio(t *testing.T) {
	t.Parallel()

	usuario := uuid.NewString()
	solicitud := SolicitudRetencion{UsuarioID: usuario, Monto: 500000, Gravamen: 2000, Moneda: MonedaCOP}
	lineas, err := lineasDeRetencion(solicitud)
	if err != nil {
		t.Fatalf("lineasDeRetencion: %v", err)
	}
	if len(lineas) != 4 {
		t.Fatalf("líneas = %d, quiero 4", len(lineas))
	}

	disponible := NombreCuentaDisponible(usuario)
	querer := []struct {
		cuenta string
		monto  int64
	}{
		{disponible, 500000},
		{NombreCuentaRetenido(usuario), -500000},
		{disponible, 2000},
		{CuentaGMFPorPagar, -2000},
	}
	var suma int64
	for i, linea := range lineas {
		if linea.CuentaID != querer[i].cuenta || linea.Monto != querer[i].monto {
			t.Errorf("línea %d = (%s, %d), quiero (%s, %d)", i, linea.CuentaID, linea.Monto, querer[i].cuenta, querer[i].monto)
		}
		if linea.Moneda != MonedaCOP {
			t.Errorf("línea %d moneda = %q, quiero %q", i, linea.Moneda, MonedaCOP)
		}
		suma += linea.Monto
	}
	if suma != 0 {
		t.Errorf("suma de las cuatro líneas = %d, quiero 0", suma)
	}

	debDisponible := 0
	for _, linea := range lineas {
		if linea.CuentaID == disponible {
			debDisponible++
		}
	}
	if debDisponible != 2 {
		t.Errorf("líneas en disponible = %d, quiero 2: el monto y el gravamen van separados", debDisponible)
	}
	if lineas[2].Monto == lineas[0].Monto {
		t.Error("el gravamen no puede ir combinado con el monto en una sola línea")
	}
}

// TestHU_NF_06_Criterio13_4_SaldoExigidoCubreMontoMasGravamen cubre que el saldo deba cubrir el monto más el gravamen (HU-NF-06, criterio 13.4).
func TestHU_NF_06_Criterio13_4_SaldoExigidoCubreMontoMasGravamen(t *testing.T) {
	t.Parallel()

	exigido, err := SaldoExigido(500000, 2000)
	if err != nil {
		t.Fatalf("SaldoExigido: %v", err)
	}
	if exigido != 502000 {
		t.Errorf("saldo exigido = %d, quiero 502000", exigido)
	}
	if exigido <= 500000 {
		t.Error("el saldo exigido no cubre el gravamen")
	}

	sinGravamen, err := SaldoExigido(500000, 0)
	if err != nil {
		t.Fatalf("SaldoExigido sin gravamen: %v", err)
	}
	if sinGravamen != 500000 {
		t.Errorf("saldo exigido sin gravamen = %d, quiero 500000", sinGravamen)
	}
}

// TestHU_NF_06_Criterio13_LaReglaQueGravaNoEsLaDelCupo cubre que la fila de GMF no le gane a la del cupo mensual (HU-NF-06, criterio 13).
func TestHU_NF_06_Criterio13_LaReglaQueGravaNoEsLaDelCupo(t *testing.T) {
	t.Parallel()

	reglas := []ReglaLimite{
		{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteOperacion, TopeUVTCentesimas: 21050},
		{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 21050},
		reglaGMFDePrueba(),
	}
	if err := evaluarLimite(reglas, uvts2026, 0, topeExentoGMF2026+1); err != nil {
		t.Fatalf("un monto sobre el tope exento de GMF pero bajo el cupo mensual: %v, quiero nil", err)
	}
}

// dbDeRetencion es un doble escrito a mano que registra las líneas que retener escribe.
type dbDeRetencion struct {
	lineas []repositorio.InsertarLineaParams
}

// Exec no lo usa la retención.
func (db *dbDeRetencion) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

// Query no lo usa la retención.
func (db *dbDeRetencion) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

// QueryRow simula el RETURNING del asiento y de cada línea.
func (db *dbDeRetencion) QueryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	if strings.Contains(sql, "journal_entry") {
		return filaConID{id: 1}
	}
	db.lineas = append(db.lineas, repositorio.InsertarLineaParams{
		EntryID:  args[0].(int64),
		CuentaID: args[1].(string),
		Monto:    args[2].(int64),
		Moneda:   args[3].(string),
	})
	return filaConID{id: int64(len(db.lineas))}
}

type filaConID struct {
	id int64
}

// Scan llena solo los destinos enteros; el ID es lo único que retener consume.
func (f filaConID) Scan(dest ...interface{}) error {
	for _, d := range dest {
		if p, ok := d.(*int64); ok {
			*p = f.id
		}
	}
	return nil
}

// TestHU_NF_06_Criterio13_3_RetenerEscribeCuatroLineasConElGravamenPropio cubre que retener escriba el gravamen como línea separada (HU-NF-06, criterio 13.3).
func TestHU_NF_06_Criterio13_3_RetenerEscribeCuatroLineasConElGravamenPropio(t *testing.T) {
	t.Parallel()

	usuario := uuid.NewString()
	db := &dbDeRetencion{}
	solicitud := SolicitudRetencion{
		TransferenciaID: uuid.New(),
		UsuarioID:       usuario,
		Monto:           500000,
		Gravamen:        2000,
		Moneda:          MonedaCOP,
		OcurridoEn:      time.Now().UTC(),
	}
	if err := retener(t.Context(), repositorio.New(db), solicitud); err != nil {
		t.Fatalf("retener: %v", err)
	}
	if len(db.lineas) != 4 {
		t.Fatalf("líneas escritas = %d, quiero 4", len(db.lineas))
	}
	var suma int64
	for _, linea := range db.lineas {
		if linea.EntryID != 1 {
			t.Errorf("la línea quedó sin el asiento: entry_id = %d, quiero 1", linea.EntryID)
		}
		suma += linea.Monto
	}
	if suma != 0 {
		t.Errorf("suma de las cuatro líneas = %d, quiero 0", suma)
	}
	if db.lineas[2].CuentaID != NombreCuentaDisponible(usuario) || db.lineas[2].Monto != 2000 {
		t.Errorf("la línea del gravamen = (%s, %d), quiero (%s, 2000)", db.lineas[2].CuentaID, db.lineas[2].Monto, NombreCuentaDisponible(usuario))
	}
	if db.lineas[3].CuentaID != CuentaGMFPorPagar || db.lineas[3].Monto != -2000 {
		t.Errorf("la contrapartida del gravamen = (%s, %d), quiero (%s, -2000)", db.lineas[3].CuentaID, db.lineas[3].Monto, CuentaGMFPorPagar)
	}
}

// TestCuentaExentaGMFEsElDisponibleDelEmisor fija la marcación de la cuenta exenta (HU-NF-06, criterio 13).
func TestCuentaExentaGMFEsElDisponibleDelEmisor(t *testing.T) {
	t.Parallel()

	usuario := uuid.NewString()
	if CuentaExentaGMF(usuario) != NombreCuentaDisponible(usuario) {
		t.Errorf("CuentaExentaGMF = %q, quiero %q", CuentaExentaGMF(usuario), NombreCuentaDisponible(usuario))
	}
}
