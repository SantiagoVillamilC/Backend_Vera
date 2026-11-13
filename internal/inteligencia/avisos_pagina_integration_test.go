//go:build integration

package inteligencia

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPaginaAvisosEmpatesPrivacidadYDescarte(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario, otro := uuid.NewString(), uuid.NewString()
	s := NuevoServicio(pool)
	ids := make([]uuid.UUID, 5)
	periodo := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	tx, e := pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), `SET LOCAL ROLE rol_inteligencia`); e != nil {
		t.Fatal(e)
	}
	for i := range ids {
		ids[i] = uuid.New()
		if _, e = tx.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto) VALUES($1,$2,$3,$4,'2026-07-01'::date,100,1,'aviso de prueba')`, ids[i], usuario, tipoDeRelleno(i), fmt.Sprintf("clave|%d", i/2)); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	vistos := map[uuid.UUID]bool{}
	for _, limite := range []int{1, 50} {
		p, err := s.AvisosDelUsuarioPaginados(t.Context(), usuario, periodo, EstadoVigentes, nil, limite)
		if err != nil || len(p.Avisos) != min(limite, len(ids)) {
			t.Fatalf("limite=%d: %d avisos, error=%v", limite, len(p.Avisos), err)
		}
	}
	var cursor *CursorDeAviso
	for {
		p, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, periodo, EstadoVigentes, cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range p.Avisos {
			if vistos[a.ID] {
				t.Fatal("aviso repetido")
			}
			vistos[a.ID] = true
		}
		if !p.HayMas {
			if p.Siguiente != "" {
				t.Fatal("cursor final")
			}
			break
		}
		c, e := DecodificarCursorDeAviso(p.Siguiente)
		if e != nil {
			t.Fatal(e)
		}
		cursor = &c
	}
	if len(vistos) != 5 {
		t.Fatal("avisos omitidos")
	}
	for _, id := range []uuid.UUID{ids[0], uuid.New()} {
		if _, e := s.Aviso(t.Context(), otro, id); !errors.Is(e, ErrAvisoNoEncontrado) {
			t.Fatal(e)
		}
		if _, e := s.DescartarAviso(t.Context(), otro, id, time.Now()); !errors.Is(e, ErrAvisoNoEncontrado) {
			t.Fatal(e)
		}
	}
	if _, e := s.Aviso(t.Context(), usuario, ids[0]); e != nil {
		t.Fatal(e)
	}
	primero, e := s.DescartarAviso(t.Context(), usuario, ids[0], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	segundo, e := s.DescartarAviso(t.Context(), usuario, ids[0], time.Now().Add(time.Hour))
	if e != nil || !primero.DescartadoEn.Equal(segundo.DescartadoEn) {
		t.Fatal("descarte no idempotente", e)
	}
	p, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, periodo, EstadoDescartados, nil, 20)
	if e != nil || len(p.Avisos) != 1 {
		t.Fatal(p, e)
	}
}

// tipoDeRelleno alterna dos tipos para que dos filas puedan compartir clave dentro del mismo
// periodo: la clave única es (usuario, tipo, clave, periodo), y el cursor ordena por clave e id.
func tipoDeRelleno(i int) string {
	if i%2 == 0 {
		return "cobro_nuevo"
	}
	return "caida_ingreso"
}

// avisoSembrado describe una fila de la bandeja con el periodo que de verdad guarda cada tipo: el
// día 1 en presupuesto_anticipado y categoria_sobre_media, la fecha del pago en pago_no_alcanza y
// cobro_nuevo, y el mes evaluado (el anterior) en caida_ingreso.
type avisoSembrado struct {
	tipo, clave, periodo string
	prioridad            int
}

func sembrarAvisosConPeriodoPropio(t *testing.T, pool *pgxpool.Pool, usuario string, filas []avisoSembrado) map[string]uuid.UUID {
	t.Helper()
	tx, e := pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, e = tx.Exec(t.Context(), `SET LOCAL ROLE rol_inteligencia`); e != nil {
		t.Fatal(e)
	}
	ids := make(map[string]uuid.UUID, len(filas))
	for _, f := range filas {
		id := uuid.New()
		ids[f.tipo+"@"+f.periodo] = id
		if _, e = tx.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto) VALUES($1,$2,$3,$4,$5::date,100,$6,'aviso de prueba')`,
			id, usuario, f.tipo, f.clave, f.periodo, f.prioridad); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	return ids
}

func tiposDe(p PaginaDeAvisos) []string {
	tipos := make([]string, 0, len(p.Avisos))
	for _, a := range p.Avisos {
		tipos = append(tipos, string(a.Tipo))
	}
	return tipos
}

// La bandeja del mes en curso filtra por el rango del mes, no por igualdad con el día 1: el periodo
// de pago_no_alcanza y cobro_nuevo es la fecha del pago. caida_ingreso pertenece al mes evaluado (el
// anterior): aparece en la bandeja de ese mes y no en la del mes en curso, igual que en el cupo y el
// resumen. Las filas de los bordes (30 de junio y 1 de agosto) no son de julio.
func TestPaginaAvisosPeriodosPropiosDeCadaTipo(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario := uuid.NewString()
	s := NuevoServicio(pool)
	julio := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	ids := sembrarAvisosConPeriodoPropio(t, pool, usuario, []avisoSembrado{
		{"pago_no_alcanza", "pago-proximo", "2026-07-20", 1},
		{"caida_ingreso", "ingreso-mensual", "2026-06-01", 2},
		{"cobro_nuevo", "cobro-nuevo", "2026-07-28", 3},
		{"presupuesto_anticipado", "gasto-total", "2026-07-01", 4},
		{"categoria_sobre_media", "transporte", "2026-07-01", 5},
		{"pago_no_alcanza", "pago-proximo", "2026-06-30", 1},
		{"cobro_nuevo", "cobro-nuevo", "2026-08-01", 3},
	})
	esperado := []string{"pago_no_alcanza", "cobro_nuevo", "presupuesto_anticipado", "categoria_sobre_media"}

	p, err := s.AvisosDelUsuarioPaginados(t.Context(), usuario, julio, EstadoVigentes, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := tiposDe(p); !slices.Equal(got, esperado) {
		t.Fatalf("bandeja de julio = %v, want %v", got, esperado)
	}

	// Paridad con el camino ?mes=, que es el que cuenta el cupo y el resumen.
	delMes, err := s.AvisosDelUsuario(t.Context(), usuario, "2026-07", julio)
	if err != nil {
		t.Fatal(err)
	}
	if len(delMes) != len(p.Avisos) {
		t.Fatalf("?mes=2026-07 trae %d y la bandeja %d: deben coincidir", len(delMes), len(p.Avisos))
	}
	for i := range delMes {
		if delMes[i].ID != p.Avisos[i].ID {
			t.Fatalf("posicion %d: ?mes= %s, bandeja %s", i, delMes[i].ID, p.Avisos[i].ID)
		}
	}

	// caida_ingreso vive en su mes.
	junio, err := s.AvisosDelUsuario(t.Context(), usuario, "2026-06", julio)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(junio, func(a AvisoEmitido) bool { return a.ID == ids["caida_ingreso@2026-06-01"] }) {
		t.Fatal("caida_ingreso no aparece en la bandeja de su mes")
	}

	// Paginación por cursor con dos por página a través de fechas distintas.
	var vistos []string
	var cursor *CursorDeAviso
	for paginas := 0; ; paginas++ {
		if paginas > 4 {
			t.Fatal("el cursor no termina")
		}
		pag, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, julio, EstadoVigentes, cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		vistos = append(vistos, tiposDe(pag)...)
		if !pag.HayMas {
			break
		}
		c, e := DecodificarCursorDeAviso(pag.Siguiente)
		if e != nil {
			t.Fatal(e)
		}
		cursor = &c
	}
	if !slices.Equal(vistos, esperado) {
		t.Fatalf("recorrido por cursor = %v, want %v", vistos, esperado)
	}
}

func TestPaginaAvisosDescartadosConPeriodoDistintoDelDiaUno(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario := uuid.NewString()
	s := NuevoServicio(pool)
	julio := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	ids := sembrarAvisosConPeriodoPropio(t, pool, usuario, []avisoSembrado{
		{"pago_no_alcanza", "pago-proximo", "2026-07-20", 1},
		{"cobro_nuevo", "cobro-nuevo", "2026-07-28", 3},
		{"presupuesto_anticipado", "gasto-total", "2026-07-01", 4},
	})
	if _, err := s.DescartarAviso(t.Context(), usuario, ids["pago_no_alcanza@2026-07-20"], time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	d, err := s.AvisosDelUsuarioPaginados(t.Context(), usuario, julio, EstadoDescartados, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := tiposDe(d); !slices.Equal(got, []string{"pago_no_alcanza"}) {
		t.Fatalf("descartados = %v, want [pago_no_alcanza]", got)
	}
	v, err := s.AvisosDelUsuarioPaginados(t.Context(), usuario, julio, EstadoVigentes, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := tiposDe(v); !slices.Equal(got, []string{"cobro_nuevo", "presupuesto_anticipado"}) {
		t.Fatalf("vigentes = %v", got)
	}
}
