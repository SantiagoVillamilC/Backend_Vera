// Comando semilla-escenario prepara un usuario exclusivo en una base de demo local.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type movimiento struct {
	fecha        time.Time
	nombre, tipo string
	centavos     int64
	mcc          string
	gmf          int64
}
type estado struct {
	Usuario        string         `json:"usuario"`
	Fecha          time.Time      `json:"fecha_escenario"`
	Asientos       []int64        `json:"asientos"`
	Transferencias []uuid.UUID    `json:"transferencias"`
	Categorias     map[string]int `json:"categorias"`
	Origenes       map[string]int `json:"origenes"`
	Avisos         map[string]int `json:"avisos"`
	Series         int            `json:"series"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// validarDSN no admite parámetros que puedan sustituir host, puerto o base.
func validarDSN(dsn string) error {
	u, e := url.Parse(dsn)
	if e != nil {
		return errors.New("DSN de demo inválido")
	}
	if (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() != "127.0.0.1" || u.Port() != "55437" || !strings.HasPrefix(u.Path, "/vera_semilla_") || len(u.Path) <= len("/vera_semilla_") || strings.Contains(u.Path[1:], "/") || u.Fragment != "" {
		return errors.New("solo se permite 127.0.0.1:55437/vera_semilla_*")
	}
	for k := range u.Query() {
		if k != "sslmode" {
			return errors.New("el DSN solo admite el parámetro sslmode")
		}
	}
	return nil
}

func plan(ahora time.Time) []movimiento {
	mes := time.Date(ahora.Year(), ahora.Month(), 1, 12, 0, 0, 0, ahora.Location())
	var out []movimiento
	agregar := func(m, d int, n, t string, pesos int64) {
		out = append(out, movimiento{fecha: mes.AddDate(0, m, d-1), nombre: n, tipo: t, centavos: pesos * 100})
	}
	agregar(-5, 1, "Saldo inicial", "deposito", 4000)
	nombres := []string{"SUPERMERCADO DEMO", "TAXI DEMO", "RESTAURANTE DEMO", "FARMACIA DEMO", "CINE DEMO"}
	for m := -5; m <= 0; m++ {
		ingreso := int64(200000)
		if m >= -1 {
			ingreso = 138000
		}
		agregar(m, 1, "Ingreso demo", "deposito", ingreso)
		if m < 0 {
			agregar(m, 20, "ENEL", "liquidacion_confirmada", -50000)
			agregar(m, 25, "INTERNET HOGAR", "liquidacion_confirmada", -89900)
		}
		for _, n := range nombres {
			agregar(m, 5, n, "liquidacion_confirmada", -10000)
		}
	}
	agregar(-1, 28, "STREAMING", "liquidacion_confirmada", -24900)
	agregar(0, 12, "STREAMING", "liquidacion_confirmada", -24900)
	agregar(0, 13, "ZXQ987654", "liquidacion_confirmada", -1000)
	// Compra grande y su reembolso: el gasto del mes supera la mediana de los meses completos y el saldo sigue positivo pero por debajo de los pagos de la ventana.
	agregar(0, 7, "TECNOLOGIA DEMO", "liquidacion_confirmada", -120000)
	agregar(0, 10, "Reembolso equipo demo", "deposito", 100000)
	// Entradas que encienden los escalones que faltaban al censo (CF-A2-03): MCC en el QR, marca
	// del catálogo, corrección en memoria privada y contraparte interna (GMF). Montos pequeños para
	// no mover el saldo ni los avisos del escenario.
	agregar(0, 14, "COMPRA QR DEMO", "liquidacion_confirmada", -500)
	out[len(out)-1].mcc = "5411"
	agregar(0, 15, "TIENDA CATALOGO DEMO", "liquidacion_confirmada", -600)
	agregar(0, 16, "TIENDA MEMORIA DEMO", "liquidacion_confirmada", -700)
	agregar(0, 17, "PAGO CON GMF DEMO", "liquidacion_confirmada", -800)
	out[len(out)-1].gmf = 80
	return out
}

// cupoDesdeEntorno lee AVISOS_CUPO igual que la API: vacío toma el default del dominio.
func cupoDesdeEntorno(valor string) (int, error) {
	if valor == "" {
		return avisos.CupoMensualPorDefecto, nil
	}
	cupo, e := strconv.Atoi(valor)
	if e != nil || cupo < 1 {
		return 0, fmt.Errorf("AVISOS_CUPO debe ser un entero positivo, llegó %q", valor)
	}
	return cupo, nil
}

// avisosDelEscenario es lo que la demo debe disparar con cupo 5. Las cuatro entradas de los
// escalones suman un quinto aviso de categoria sobre la media; con cupo 5 caben todos.
var avisosDelEscenario = map[string]int{
	"caida_ingreso":          1,
	"categoria_sobre_media":  1,
	"cobro_nuevo":            1,
	"pago_no_alcanza":        1,
	"presupuesto_anticipado": 1,
}

// categoriasDelEscenario y origenesDelEscenario son el censo que la tarjeta publica como evidencia.
var (
	categoriasDelEscenario = map[string]int{
		"alimentos_mercado":             7,
		"compras":                       2,
		"comisiones_costos_financieros": 9,
		"entretenimiento":               7,
		"restaurantes_domicilios":       6,
		"salud":                         6,
		"sin_categoria":                 13,
		"transporte":                    6,
	}
	origenesDelEscenario = map[string]int{
		"catalogo":            1,
		"contraparte_interna": 1,
		"lexico":              39,
		"mcc":                 1,
		"memoria":             1,
		"sin_categoria":       13,
	}
)

// cumpleAceptacion exige los cuatro avisos, 52 asientos, 7 series maduras y el censo exacto de
// categorías y orígenes que declara la evidencia. Otra repartición con las mismas claves no pasa.
func cumpleAceptacion(st estado) bool {
	return len(st.Asientos) == 56 && st.Series == 7 &&
		censoIgual(st.Avisos, avisosDelEscenario) &&
		censoIgual(st.Categorias, categoriasDelEscenario) &&
		censoIgual(st.Origenes, origenesDelEscenario)
}

func censoIgual(obtenido, esperado map[string]int) bool {
	if len(obtenido) != len(esperado) {
		return false
	}
	for clave, n := range esperado {
		if obtenido[clave] != n {
			return false
		}
	}
	return true
}

func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if e := validarDSN(dsn); e != nil {
		return e
	}
	usuario, e := uuid.Parse(os.Getenv("DEMO_USUARIO"))
	if e != nil || usuario == uuid.Nil {
		return errors.New("DEMO_USUARIO debe ser un UUID no nulo exclusivo de demo")
	}
	cupo, e := cupoDesdeEntorno(os.Getenv("AVISOS_CUPO"))
	if e != nil {
		return e
	}
	reiniciar := os.Getenv("REINICIAR")
	if reiniciar != "" && reiniciar != "no" && reiniciar != "si" {
		return errors.New("REINICIAR debe ser si o no")
	}
	zona := time.FixedZone("America/Bogota", -5*3600)
	hoy := time.Now().In(zona)
	ahora := time.Date(hoy.Year(), hoy.Month(), 19, 12, 0, 0, 0, zona)
	if valor := os.Getenv("DEMO_FECHA"); valor != "" {
		ahora, e = time.ParseInLocation("2006-01-02", valor, zona)
		if e != nil || ahora.Day() != 19 {
			return errors.New("DEMO_FECHA debe ser YYYY-MM-19")
		}
		ahora = ahora.Add(12 * time.Hour)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return errors.New("no se pudo configurar la base de demo")
	}
	defer pool.Close()
	conn, e := pool.Acquire(ctx)
	if e != nil {
		return errors.New("no se pudo conectar a la base de demo")
	}
	defer conn.Release()
	// Candado de sesión: cubre siembra, enriquecimiento, avisos y evidencia.
	if _, e = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", "escenario:"+usuario.String()); e != nil {
		return e
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", "escenario:"+usuario.String())
	}()
	st, e := sembrar(ctx, conn, usuario, ahora, reiniciar == "si")
	if e != nil {
		return e
	}
	lexico, e := inteligencia.CargarLexicoV2()
	if e != nil {
		return e
	}
	servicio := inteligencia.NuevoServicio(pool, inteligencia.ConLexico(lexico), inteligencia.ConCupoDeAvisos(cupo))
	if e = servicio.CargarCatalogo(ctx); e != nil {
		return e
	}
	st.Series, e = servicio.ProcesarEscenario(ctx, usuario.String(), st.Asientos, st.Fecha)
	if e != nil {
		return e
	}
	st.Categorias, e = conteo(ctx, pool, `SELECT categoria,count(*) FROM inteligencia.movimiento_enriquecido WHERE usuario_id=$1 GROUP BY 1`, st.Usuario)
	if e != nil {
		return e
	}
	st.Origenes, e = conteo(ctx, pool, `SELECT origen_categoria,count(*) FROM inteligencia.movimiento_enriquecido WHERE usuario_id=$1 GROUP BY 1`, st.Usuario)
	if e != nil {
		return e
	}
	st.Avisos, e = conteo(ctx, pool, `SELECT tipo,count(*) FROM inteligencia.aviso WHERE usuario_id=$1 GROUP BY 1`, st.Usuario)
	if e != nil {
		return e
	}
	if !cumpleAceptacion(st) {
		return fmt.Errorf("escenario no cumple aceptación: avisos=%v series=%d categorías=%v orígenes=%v asientos=%d", st.Avisos, st.Series, st.Categorias, st.Origenes, len(st.Asientos))
	}
	b, e := json.MarshalIndent(st, "", "  ")
	if e != nil {
		return e
	}
	ruta := os.Getenv("ESTADO")
	if ruta == "" {
		ruta = "DEMO-ESCENARIO-STATE.json"
	}
	if e = os.WriteFile(ruta+".tmp", append(b, '\n'), 0600); e != nil { //nolint:gosec // ESTADO es una ruta elegida por el operador local del CLI, no entrada HTTP.
		return e
	}
	if e = os.Rename(ruta+".tmp", ruta); e != nil { //nolint:gosec // Destino elegido explícitamente por el operador del CLI.
		return e
	}
	fmt.Println(string(b))
	return nil
}

func sembrar(ctx context.Context, conn *pgxpool.Conn, usuario uuid.UUID, ahora time.Time, reiniciar bool) (estado, error) {
	st := estado{Usuario: usuario.String(), Fecha: ahora, Asientos: []int64{}, Transferencias: []uuid.UUID{}}
	tx, e := conn.Begin(ctx)
	if e != nil {
		return st, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Manifiesto transaccional: una reejecución conserva la fecha original, incluso otro mes.
	e = tx.QueryRow(ctx, `SELECT fecha,asientos,transferencias FROM public.demo_escenario WHERE usuario_id=$1`, usuario).Scan(&st.Fecha, &st.Asientos, &st.Transferencias)
	existe := e == nil
	if existe {
		st.Fecha = st.Fecha.In(ahora.Location())
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return st, fmt.Errorf("no se pudo leer el manifiesto public.demo_escenario, revisa las migraciones: %w", e)
	}
	cuenta := "pasivo:usuario:" + usuario.String() + ":disponible"
	var ajenos int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM nucleo.posting WHERE cuenta_id LIKE $1 AND NOT(entry_id=ANY($2::bigint[]))`, "pasivo:usuario:"+usuario.String()+":%", st.Asientos).Scan(&ajenos)
	if e != nil {
		return st, e
	}
	if ajenos != 0 {
		return st, errors.New("el usuario tiene asientos ajenos al escenario; usa otro DEMO_USUARIO")
	}
	// Entradas de los escalones del censo: una marca del catálogo v3 y una corrección en memoria
	// privada del usuario de demo. Idempotentes: reejecutar actualiza la categoría, no duplica.
	catalogo := inteligencia.NormalizarNombre("TIENDA CATALOGO DEMO")
	if _, e = tx.Exec(ctx, `INSERT INTO inteligencia.catalogo_marca(nombre_normalizado,categoria,llave,origen,version) VALUES($1,'compras',NULL,'escenario','v3') ON CONFLICT (nombre_normalizado) DO UPDATE SET categoria=excluded.categoria`, catalogo); e != nil {
		return st, e
	}
	memoria := inteligencia.NormalizarNombre("TIENDA MEMORIA DEMO")
	if _, e = tx.Exec(ctx, `INSERT INTO inteligencia.memoria_correccion(usuario_id,tipo_destino,nivel,valor,categoria,actualizado_en) VALUES($1,'comercio','nombre',$2,'entretenimiento',now()) ON CONFLICT (usuario_id,tipo_destino,nivel,valor) DO UPDATE SET categoria=excluded.categoria, actualizado_en=now()`, usuario.String(), memoria); e != nil {
		return st, e
	}
	if existe && !reiniciar {
		return st, tx.Commit(ctx)
	}
	if reiniciar {
		for _, consulta := range []string{`DELETE FROM inteligencia.aviso WHERE usuario_id=$1`, `DELETE FROM inteligencia.movimiento_enriquecido WHERE usuario_id=$1`} {
			if _, e = tx.Exec(ctx, consulta, usuario.String()); e != nil {
				return st, e
			}
		}
		if _, e = tx.Exec(ctx, `DELETE FROM nucleo.posting WHERE entry_id=ANY($1::bigint[])`, st.Asientos); e != nil {
			return st, e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM nucleo.journal_entry WHERE id=ANY($1::bigint[])`, st.Asientos); e != nil {
			return st, e
		}
	}
	st.Fecha = ahora
	st.Asientos = []int64{}
	st.Transferencias = []uuid.UUID{}
	externa := "pasivo:liquidacion_pendiente:demo-" + usuario.String()
	custodia := "activo:custodia:demo-" + usuario.String()
	for _, c := range []struct{ id, naturaleza, clase string }{{cuenta, "pasivo", "usuario"}, {externa, "pasivo", "liquidacion"}, {custodia, "activo", "custodia"}} {
		if _, e = tx.Exec(ctx, `INSERT INTO nucleo.cuenta(id,naturaleza,clase,titular_id) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, c.id, c.naturaleza, c.clase, usuario.String()); e != nil {
			return st, e
		}
	}
	for i, m := range plan(ahora) {
		transferencia := uuid.NewSHA1(usuario, []byte(fmt.Sprintf("escenario-v1:%s:%d", ahora.Format("2006-01"), i)))
		var id int64
		if e = tx.QueryRow(ctx, `INSERT INTO nucleo.journal_entry(tipo,transferencia_id,ocurrido_en,descripcion,mcc) VALUES($1,$2,$3,$4,NULLIF($5,'')) RETURNING id`, m.tipo, transferencia, m.fecha, m.nombre, m.mcc).Scan(&id); e != nil {
			return st, e
		}
		contra := externa
		if m.tipo == "deposito" {
			contra = custodia
		}
		if m.gmf > 0 {
			if _, e = tx.Exec(ctx, `INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda) VALUES($1,$2,$3,'COP'),($1,$4,$5,'COP'),($1,'pasivo:gmf_por_pagar',$6,'COP')`, id, cuenta, -m.centavos, contra, m.centavos-m.gmf, m.gmf); e != nil {
				return st, e
			}
		} else if _, e = tx.Exec(ctx, `INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda) VALUES($1,$2,$3,'COP'),($1,$4,$5,'COP')`, id, cuenta, -m.centavos, contra, m.centavos); e != nil {
			return st, e
		}
		st.Asientos = append(st.Asientos, id)
		st.Transferencias = append(st.Transferencias, transferencia)
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.demo_escenario VALUES($1,$2,$3,$4) ON CONFLICT(usuario_id) DO UPDATE SET fecha=excluded.fecha,asientos=excluded.asientos,transferencias=excluded.transferencias`, usuario, st.Fecha, st.Asientos, st.Transferencias)
	if e != nil {
		return st, e
	}
	return st, tx.Commit(ctx)
}

func conteo(ctx context.Context, pool *pgxpool.Pool, sql, usuario string) (map[string]int, error) {
	filas, e := pool.Query(ctx, sql, usuario)
	if e != nil {
		return nil, e
	}
	defer filas.Close()
	resultado := map[string]int{}
	for filas.Next() {
		var k string
		var n int
		if e = filas.Scan(&k, &n); e != nil {
			return nil, e
		}
		resultado[k] = n
	}
	return resultado, filas.Err()
}
