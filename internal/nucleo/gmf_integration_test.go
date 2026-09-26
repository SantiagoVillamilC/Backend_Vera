//go:build integration

package nucleo

import (
	"context"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ejecutarVerificarLimiteConGravamen corre el motor dentro de una transacción y devuelve el gravamen resuelto.
func ejecutarVerificarLimiteConGravamen(ctx context.Context, pool *pgxpool.Pool, usuarioID string, monto Centavos, momento time.Time) (Centavos, error) {
	var gravamen Centavos
	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			g, err := verificarLimite(ctx, q, usuarioID, NivelKYCDepositoBajoMonto, monto, momento)
			gravamen = g
			return struct{}{}, err
		})
	return gravamen, err
}

type lineaDeAsiento struct {
	cuenta string
	monto  int64
}

// lineasDeTransferencia lee las líneas del asiento de retención ligado a una transferencia.
func lineasDeTransferencia(t *testing.T, pool *pgxpool.Pool, transferenciaID uuid.UUID) []lineaDeAsiento {
	t.Helper()

	var lineas []lineaDeAsiento
	if _, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx, `SELECT p.cuenta_id, p.monto
				  FROM nucleo.posting p
				  JOIN nucleo.journal_entry je ON je.id = p.entry_id
				 WHERE je.transferencia_id = $1
				 ORDER BY p.id`, pgtype.UUID{Bytes: transferenciaID, Valid: true})
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var linea lineaDeAsiento
				if err := rows.Scan(&linea.cuenta, &linea.monto); err != nil {
					return struct{}{}, err
				}
				lineas = append(lineas, linea)
			}
			return struct{}{}, rows.Err()
		}); err != nil {
		t.Fatalf("leer las líneas de la transferencia %s: %v", transferenciaID, err)
	}
	return lineas
}

// ejecutarRetener escribe la retención como lo hará el flujo del envío dentro de su transacción.
func ejecutarRetener(ctx context.Context, pool *pgxpool.Pool, solicitud SolicitudRetencion) error {
	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, retener(ctx, q, solicitud)
		})
	return err
}

// TestHU_NF_06_Criterio13_1_EnvioExentoQuedaDeDosLineas cubre que un envío dentro del tope exento no retiene GMF (HU-NF-06, criterio 13.1).
func TestHU_NF_06_Criterio13_1_EnvioExentoQuedaDeDosLineas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeExentoGMF2026-1000, EstadoTransferenciaReservada, momento)

	gravamen, err := ejecutarVerificarLimiteConGravamen(ctx, pool, usuario, 1000, momento)
	if err != nil {
		t.Fatalf("verificar límite: %v, quiero nil", err)
	}
	if gravamen != 0 {
		t.Fatalf("gravamen = %d, quiero 0", gravamen)
	}

	transferenciaID := uuid.New()
	solicitud := SolicitudRetencion{TransferenciaID: transferenciaID, UsuarioID: usuario, Monto: 1000, Gravamen: gravamen, Moneda: MonedaCOP, OcurridoEn: momento}
	if err := ejecutarRetener(ctx, pool, solicitud); err != nil {
		t.Fatalf("retener: %v", err)
	}

	lineas := lineasDeTransferencia(t, pool, transferenciaID)
	if len(lineas) != 2 {
		t.Fatalf("líneas = %d, quiero 2", len(lineas))
	}
	if lineas[0].cuenta != NombreCuentaDisponible(usuario) || lineas[0].monto != 1000 {
		t.Errorf("línea 0 = (%s, %d), quiero (%s, 1000)", lineas[0].cuenta, lineas[0].monto, NombreCuentaDisponible(usuario))
	}
	if lineas[1].cuenta != NombreCuentaRetenido(usuario) || lineas[1].monto != -1000 {
		t.Errorf("línea 1 = (%s, %d), quiero (%s, -1000)", lineas[1].cuenta, lineas[1].monto, NombreCuentaRetenido(usuario))
	}
	t.Logf("envío exento: retención de dos líneas, sin gravamen")
}

// TestHU_NF_06_Criterio13_2_RetencionConGMFEscribeCuatroLineasQueCuadran cubre el 4x1000 sobre el exceso y la retención de cuatro líneas (HU-NF-06, criterio 13.2 y 13.3).
func TestHU_NF_06_Criterio13_2_RetencionConGMFEscribeCuatroLineasQueCuadran(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeExentoGMF2026, EstadoTransferenciaReservada, momento)

	const monto = Centavos(1000000)
	gravamen, err := ejecutarVerificarLimiteConGravamen(ctx, pool, usuario, monto, momento)
	if err != nil {
		t.Fatalf("verificar límite: %v, quiero nil", err)
	}
	if gravamen != 4000 {
		t.Fatalf("gravamen = %d, quiero 4000: el 4x1000 solo cae sobre el millón que excede las 65 UVT", gravamen)
	}

	transferenciaID := uuid.New()
	solicitud := SolicitudRetencion{TransferenciaID: transferenciaID, UsuarioID: usuario, Monto: monto, Gravamen: gravamen, Moneda: MonedaCOP, OcurridoEn: momento}
	if err := ejecutarRetener(ctx, pool, solicitud); err != nil {
		t.Fatalf("retener: %v", err)
	}

	lineas := lineasDeTransferencia(t, pool, transferenciaID)
	if len(lineas) != 4 {
		t.Fatalf("líneas = %d, quiero 4", len(lineas))
	}
	querer := []lineaDeAsiento{
		{NombreCuentaDisponible(usuario), 1000000},
		{NombreCuentaRetenido(usuario), -1000000},
		{NombreCuentaDisponible(usuario), 4000},
		{CuentaGMFPorPagar, -4000},
	}
	var suma int64
	for i, linea := range lineas {
		if linea != querer[i] {
			t.Errorf("línea %d = %+v, quiero %+v", i, linea, querer[i])
		}
		suma += linea.monto
	}
	if suma != 0 {
		t.Errorf("suma de las cuatro líneas = %d, quiero 0", suma)
	}

	deDisponible := 0
	for _, linea := range lineas {
		if linea.cuenta == NombreCuentaDisponible(usuario) {
			deDisponible++
		}
	}
	if deDisponible != 2 {
		t.Errorf("líneas en disponible = %d, quiero 2: el gravamen va como línea propia", deDisponible)
	}

	verificarApendiceCSinFilas(t, pool)
	t.Logf("retención con GMF: cuatro líneas, suma cero y gravamen propio por 4000 centavos")
}

// TestHU_NF_10_Criterio9_2_AcumuladoSumaIntraentidadYPorLlave cubre que el acumulado mensual de la cuenta marcada sume las dos historias (HU-NF-10, criterio 9.2).
func TestHU_NF_10_Criterio9_2_AcumuladoSumaIntraentidadYPorLlave(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuario := uuid.NewString()
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mesAnterior := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeExentoGMF2026-125, EstadoTransferenciaReservada, momento)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "bre-b:"+uuid.NewString(), 125, EstadoTransferenciaReservada, momento)
	insertarTransferenciaDePrueba(t, pool, usuario, cuenta, "intra:"+uuid.NewString(), topeExentoGMF2026, EstadoTransferenciaReservada, mesAnterior)

	const monto = Centavos(125)
	gravamen, err := ejecutarVerificarLimiteConGravamen(ctx, pool, usuario, monto, momento)
	if err != nil {
		t.Fatalf("verificar límite: %v, quiero nil", err)
	}
	if gravamen != 1 {
		t.Fatalf("gravamen = %d, quiero 1: solo suman el intraentidad y el de llave del mes", gravamen)
	}
	t.Logf("el acumulado sumó el envío intraentidad y el de llave del mes, y excluyó el del mes anterior")
}

// verificarApendiceCSinFilas comprueba que no haya asientos descuadrados ni de una sola línea.
func verificarApendiceCSinFilas(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	consultas := map[string]string{
		"asientos descuadrados": `SELECT entry_id FROM nucleo.posting GROUP BY entry_id HAVING SUM(monto) <> 0`,
		"asientos de una linea": `SELECT je.id FROM nucleo.journal_entry je LEFT JOIN nucleo.posting p ON p.entry_id = je.id GROUP BY je.id HAVING COUNT(p.id) < 2`,
	}
	for nombre, sentencia := range consultas {
		if _, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
			func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
				rows, err := tx.Query(ctx, sentencia)
				if err != nil {
					return struct{}{}, err
				}
				defer rows.Close()
				filas := 0
				for rows.Next() {
					filas++
					var id int64
					if err := rows.Scan(&id); err != nil {
						return struct{}{}, err
					}
				}
				if filas != 0 {
					t.Errorf("%s devolvió %d filas, quiero 0", nombre, filas)
				}
				return struct{}{}, rows.Err()
			}); err != nil {
			t.Fatalf("consulta %s: %v", nombre, err)
		}
	}
}
