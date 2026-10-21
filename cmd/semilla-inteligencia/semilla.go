package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Semilla es la semilla fija del banco de medición. Cambiarla cambia los datos, nunca al revés.
const Semilla uint64 = 20261020

// PuertoDesechable es el único puerto contra el que el sembrador acepta escribir.
const PuertoDesechable = 55437

// prefijoDeBase es el prefijo obligatorio del nombre de la base de medición.
const prefijoDeBase = "vera_semilla_"

// espacioDeUsuarios deriva los identificadores del banco sintético. Es fijo para que dos corridas
// produzcan exactamente los mismos usuarios.
var espacioDeUsuarios = uuid.MustParse("7a1d1c62-3f5b-5d7e-9d21-0c1b2a3f4e55")

// comercios es la lista cerrada de nombres ficticios. No hay ningún nombre real.
var comercios = []string{
	"Abasto 07", "Café Norte 12", "Panadería El Roble 3", "Ferretería Sur 21", "Droguería Central 5",
	"Restaurante La Loma 9", "Mercado Vecino 14", "Tienda Esquina 2", "Licorera Buen Paso 8",
	"Papelería El Lápiz 11", "Heladería Pinar 4", "Sastrería Norte 17", "Taller Rueda 6",
	"Floristería Jardín 19", "Verdulería El Huerto 10", "Carnes Don Beto 15", "Bicicletería Andar 13",
	"Lavandería Burbuja 1", "Panadería Trigo 16", "Cafetería Alba 18", "Pescadería Marea 20",
	"Tienda Monte 22", "Mercado Ruta 23", "Restaurante Fogón 24", "Droguería Vida 25",
	"Ferretería Tornillo 26", "Papelería Folio 27", "Heladería Copo 28", "Floristería Rosa 29",
	"Taller Motor 30", "Carnes La Vaca 31", "Verdulería Huerta 32", "Bicicletería Pedal 33",
	"Lavandería Jabón 34", "Cafetería Grano 35", "Tienda Barrio 36", "Mercado Plaza 37",
	"Restaurante Brasa 38", "Droguería Salud 39", "Ferretería Clavo 40",
}

// Opciones describe el banco que se va a sembrar.
type Opciones struct {
	DSN            string
	Semilla        uint64
	Usuarios       int
	Meses          int
	AsientosExtra  int
	RutaTokens     string
	PermitirRemoto bool
}

// Informe resume lo sembrado.
type Informe struct {
	Usuarios        int
	Asientos        int
	Lineas          int
	Cuentas         int
	PrimerAsiento   int64
	UltimoAsiento   int64
	HuellaDelLedger string
}

// TokenDePrueba es un access firmado para un usuario sintético del banco.
type TokenDePrueba struct {
	Usuario string `json:"usuario"`
	Token   string `json:"token"`
}

// VerificarDSN impide que el sembrador escriba en una base que no sea la desechable.
func VerificarDSN(dsn string, _ uint64, permitirRemoto bool) error {
	return database.DSNLocalDeMedicion(dsn, strconv.Itoa(PuertoDesechable), prefijoDeBase, permitirRemoto)
}

// azar es un xorshift64* propio: la secuencia no depende de la versión de Go.
type azar struct {
	estado uint64
}

func nuevoAzar(semilla uint64) *azar {
	estado := semilla + 0x9e3779b97f4a7c15
	estado = (estado ^ (estado >> 30)) * 0xbf58476d1ce4e5b9
	estado = (estado ^ (estado >> 27)) * 0x94d049bb133111eb
	return &azar{estado: estado ^ (estado >> 31)}
}

func (a *azar) siguiente() uint64 {
	x := a.estado
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	a.estado = x
	return x * 2685821657736338717
}

// indice devuelve un entero en [0, tope).
func (a *azar) indice(tope int) int {
	if tope <= 0 {
		return 0
	}
	return int(a.siguiente() % uint64(tope)) //nolint:gosec // el módulo acota el valor a [0, tope).
}

// entre devuelve un valor en [minimo, maximo] en centavos.
func (a *azar) entre(minimo, maximo int64) int64 {
	if maximo <= minimo {
		return minimo
	}
	rango := uint64(maximo - minimo + 1)       //nolint:gosec // minimo es positivo en todos los llamados.
	return minimo + int64(a.siguiente()%rango) //nolint:gosec // el módulo acota el valor a [0, rango).
}

// usuarioDeIndice deriva el uuid del usuario i de forma estable.
func usuarioDeIndice(semilla uint64, indice int) uuid.UUID {
	return uuid.NewSHA1(espacioDeUsuarios, []byte(fmt.Sprintf("semilla:%d:usuario:%d", semilla, indice)))
}

// cuentaDeUsuario es el identificador de la subcuenta del usuario.
func cuentaDeUsuario(usuario uuid.UUID, sufijo string) string {
	return "pasivo:usuario:" + usuario.String() + ":" + sufijo
}

// cuentaDeComercio es el identificador de la cuenta de liquidación del comercio.
func cuentaDeComercio(indice int) string {
	return "pasivo:liquidacion_pendiente:comercio-" + strconv.Itoa(indice)
}

// asientoSembrado es una fila de la tabla temporal de asientos.
type asientoSembrado struct {
	indice        int
	tipo          string
	transferencia uuid.UUID
	ocurrido      time.Time
	descripcion   string
}

// lineaSembrada es una fila de la tabla temporal de líneas.
type lineaSembrada struct {
	indice int
	cuenta string
	monto  int64
}

// planDelBanco genera el ledger sintético completo, sin tocar la base.
func planDelBanco(opciones Opciones) ([]asientoSembrado, []lineaSembrada) {
	generador := nuevoAzar(opciones.Semilla)
	meses := opciones.Meses
	if meses <= 0 {
		meses = 10
	}
	usuarios := opciones.Usuarios
	if usuarios <= 0 {
		usuarios = 5000
	}
	zona := time.FixedZone("America/Bogota", -5*60*60)
	asientos := make([]asientoSembrado, 0, usuarios*meses*3+opciones.AsientosExtra)
	lineas := make([]lineaSembrada, 0, usuarios*meses*6+opciones.AsientosExtra*2)
	indice := 0
	agregar := func(tipo string, ocurrido time.Time, descripcion string, apuntes [][2]any) {
		transferencia := uuid.NewSHA1(espacioDeUsuarios,
			[]byte(fmt.Sprintf("semilla:%d:asiento:%d", opciones.Semilla, indice)))
		asientos = append(asientos, asientoSembrado{
			indice: indice, tipo: tipo, transferencia: transferencia,
			ocurrido: ocurrido, descripcion: descripcion,
		})
		for _, apunte := range apuntes {
			lineas = append(lineas, lineaSembrada{
				indice: indice, cuenta: apunte[0].(string), monto: apunte[1].(int64),
			})
		}
		indice++
	}
	for i := range usuarios {
		usuario := usuarioDeIndice(opciones.Semilla, i)
		siguiente := usuarioDeIndice(opciones.Semilla, (i+1)%usuarios)
		disponible := cuentaDeUsuario(usuario, "disponible")
		destinoInterno := cuentaDeUsuario(siguiente, "disponible")
		for mes := range meses {
			ocurrido := time.Date(2026, time.Month(1+mes), 5, 12, 0, 0, 0, zona)
			ingreso := generador.entre(2_000_000_00, 6_000_000_00)
			agregar("deposito", ocurrido, "fondeo",
				[][2]any{{"activo:custodia:banco", ingreso}, {disponible, -ingreso}})
			gasto := generador.entre(300_000_00, 1_500_000_00)
			comercio := cuentaDeComercio(generador.indice(len(comercios)))
			agregar("liquidacion_confirmada",
				ocurrido.AddDate(0, 0, 10+int(generador.siguiente()%8)),
				"Compra en "+comercios[generador.indice(len(comercios))],
				[][2]any{{disponible, gasto}, {comercio, -gasto}})
			transferencia := generador.entre(50_000_00, 300_000_00)
			agregar("transferencia_interna",
				ocurrido.AddDate(0, 0, 20+int(generador.siguiente()%5)),
				"Envío a contacto",
				[][2]any{{disponible, transferencia}, {destinoInterno, -transferencia}})
		}
	}
	for i := range opciones.AsientosExtra {
		usuario := usuarioDeIndice(opciones.Semilla, i%usuarios)
		ocurrido := time.Date(2026, time.October, 15, 12, 0, 0, 0, zona).Add(time.Duration(i) * time.Minute)
		monto := generador.entre(20_000_00, 90_000_00)
		agregar("liquidacion_confirmada", ocurrido, "Compra en "+comercios[i%len(comercios)],
			[][2]any{{cuentaDeUsuario(usuario, "disponible"), monto},
				{cuentaDeComercio(i % len(comercios)), -monto}})
	}
	return asientos, lineas
}

// Sembrar escribe el ledger sintético en el banco desechable y devuelve su informe.
func Sembrar(ctx context.Context, pool *pgxpool.Pool, opciones Opciones) (Informe, error) {
	asientos, lineas := planDelBanco(opciones)
	informe := Informe{
		Usuarios: opciones.Usuarios,
		Asientos: len(asientos),
		Lineas:   len(lineas),
	}
	if opciones.Usuarios <= 0 {
		informe.Usuarios = 5000
	}
	conexion, err := pool.Acquire(ctx)
	if err != nil {
		return Informe{}, fmt.Errorf("semilla: adquirir conexión: %w", err)
	}
	defer conexion.Release()
	tx, err := conexion.Begin(ctx)
	if err != nil {
		return Informe{}, fmt.Errorf("semilla: abrir transacción: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
CREATE TEMP TABLE semilla_cuenta (id text PRIMARY KEY, naturaleza text NOT NULL, clase text NOT NULL, titular_id text) ON COMMIT DROP;
CREATE TEMP TABLE semilla_asiento (indice int PRIMARY KEY, tipo text NOT NULL, transferencia uuid NOT NULL,
    ocurrido timestamptz NOT NULL, descripcion text) ON COMMIT DROP;
CREATE TEMP TABLE semilla_linea (indice int NOT NULL, cuenta text NOT NULL, monto bigint NOT NULL) ON COMMIT DROP;`); err != nil {
		return Informe{}, fmt.Errorf("semilla: crear tablas temporales: %w", err)
	}

	cuentas := [][3]string{{"activo:custodia:banco", "activo", "custodio"}}
	for i := range comercios {
		cuentas = append(cuentas, [3]string{cuentaDeComercio(i), "pasivo", "comercio"})
	}
	for i := range informe.Usuarios {
		usuario := usuarioDeIndice(opciones.Semilla, i).String()
		for _, sufijo := range []string{"disponible", "retenido"} {
			cuentas = append(cuentas, [3]string{
				"pasivo:usuario:" + usuario + ":" + sufijo, "pasivo", "usuario",
			})
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"semilla_cuenta"}, []string{"id", "naturaleza", "clase", "titular_id"},
		pgx.CopyFromSlice(len(cuentas), func(i int) ([]any, error) {
			titular := any(nil)
			if cuentas[i][2] == "usuario" {
				partes := strings.Split(cuentas[i][0], ":")
				titular = partes[2]
			}
			return []any{cuentas[i][0], cuentas[i][1], cuentas[i][2], titular}, nil
		})); err != nil {
		return Informe{}, fmt.Errorf("semilla: copiar cuentas: %w", err)
	}
	informe.Cuentas = len(cuentas)

	if err := copiarAsientos(ctx, tx, asientos, lineas); err != nil {
		return Informe{}, err
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO nucleo.cuenta (id, naturaleza, clase, titular_id)
SELECT id, naturaleza, clase, titular_id FROM semilla_cuenta
ON CONFLICT (id) DO NOTHING`); err != nil {
		return Informe{}, fmt.Errorf("semilla: insertar cuentas: %w", err)
	}

	if err := insertarMovimientos(ctx, tx); err != nil {
		return Informe{}, err
	}

	if err := tx.QueryRow(ctx, `SELECT COALESCE(min(id), 0), COALESCE(max(id), 0) FROM nucleo.journal_entry`).
		Scan(&informe.PrimerAsiento, &informe.UltimoAsiento); err != nil {
		return Informe{}, fmt.Errorf("semilla: leer los asientos sembrados: %w", err)
	}
	huella, err := huellaDelLedger(ctx, tx)
	if err != nil {
		return Informe{}, err
	}
	informe.HuellaDelLedger = huella
	if err := tx.Commit(ctx); err != nil {
		return Informe{}, fmt.Errorf("semilla: confirmar el banco: %w", err)
	}
	return informe, nil
}

func copiarAsientos(ctx context.Context, tx pgx.Tx, asientos []asientoSembrado,
	lineas []lineaSembrada) error {
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"semilla_asiento"},
		[]string{"indice", "tipo", "transferencia", "ocurrido", "descripcion"},
		pgx.CopyFromSlice(len(asientos), func(i int) ([]any, error) {
			return []any{asientos[i].indice, asientos[i].tipo, asientos[i].transferencia,
				asientos[i].ocurrido, asientos[i].descripcion}, nil
		})); err != nil {
		return fmt.Errorf("semilla: copiar asientos: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"semilla_linea"},
		[]string{"indice", "cuenta", "monto"},
		pgx.CopyFromSlice(len(lineas), func(i int) ([]any, error) {
			return []any{lineas[i].indice, lineas[i].cuenta, lineas[i].monto}, nil
		})); err != nil {
		return fmt.Errorf("semilla: copiar líneas: %w", err)
	}
	return nil
}

// insertarMovimientos crea los asientos y sus líneas en una sola sentencia: el CTE devuelve los
// identificadores recién creados y las líneas se unen por la transferencia, que es única por asiento.
func insertarMovimientos(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
WITH nuevos AS (
    INSERT INTO nucleo.journal_entry (tipo, transferencia_id, ocurrido_en, descripcion)
    SELECT tipo, transferencia, ocurrido, descripcion FROM semilla_asiento ORDER BY indice
    RETURNING id, transferencia_id
), por_transferencia AS (
    SELECT id, transferencia_id FROM nuevos
)
INSERT INTO nucleo.posting (entry_id, cuenta_id, monto, moneda)
SELECT por_transferencia.id, linea.cuenta, linea.monto, 'COP'
  FROM semilla_linea linea
  JOIN semilla_asiento asiento ON asiento.indice = linea.indice
  JOIN por_transferencia ON por_transferencia.transferencia_id = asiento.transferencia`); err != nil {
		return fmt.Errorf("semilla: insertar asientos y líneas: %w", err)
	}
	return nil
}

// huellaDelLedger resume el ledger sin traerlo a memoria y detecta altas, bajas y cambios.
func huellaDelLedger(ctx context.Context, tx pgx.Tx) (string, error) {
	var huella string
	if err := tx.QueryRow(ctx, `
SELECT md5(
    COALESCE((SELECT count(*)::text FROM nucleo.cuenta), '0') || '|' ||
    COALESCE((SELECT sum(hashtextextended(t::text, 0))::text FROM nucleo.cuenta t), '0') || '|' ||
    COALESCE((SELECT count(*)::text FROM nucleo.journal_entry), '0') || '|' ||
    COALESCE((SELECT sum(hashtextextended(t::text, 0))::text FROM nucleo.journal_entry t), '0') || '|' ||
    COALESCE((SELECT count(*)::text FROM nucleo.posting), '0') || '|' ||
    COALESCE((SELECT sum(hashtextextended(t::text, 0))::text FROM nucleo.posting t), '0')
)`).Scan(&huella); err != nil {
		return "", fmt.Errorf("semilla: calcular la huella del ledger: %w", err)
	}
	return huella, nil
}

// AcunarTokens firma un access por usuario con el mismo firmador del API.
func AcunarTokens(secreto, emisor, audiencia string, semilla uint64, usuarios int) ([]TokenDePrueba, error) {
	firmador, err := auth.NuevoTokensDeAcceso([]byte(secreto), emisor, audiencia)
	if err != nil {
		return nil, fmt.Errorf("semilla: construir el firmador: %w", err)
	}
	tokens := make([]TokenDePrueba, 0, usuarios)
	for i := range usuarios {
		usuario := usuarioDeIndice(semilla, i)
		sesion := uuid.NewSHA1(espacioDeUsuarios,
			[]byte(fmt.Sprintf("semilla:%d:sesion:%d", semilla, i)))
		firmado, err := firmador.Firmar(auth.ClaimsAccess{
			ParteID:  usuario,
			SesionID: sesion,
			Contexto: auth.ContextoUsuario,
			Rol:      "titular",
		})
		if err != nil {
			return nil, fmt.Errorf("semilla: firmar el access de %s: %w", usuario, err)
		}
		tokens = append(tokens, TokenDePrueba{Usuario: usuario.String(), Token: firmado})
	}
	return tokens, nil
}
