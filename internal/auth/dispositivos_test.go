package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// denylistEspia anota cada publicación en el mismo log que la transacción falsa para probar el orden.
type denylistEspia struct {
	log *[]string
	err error
}

func (d denylistEspia) Revocar(_ context.Context, sesionID uuid.UUID, _ time.Duration) error {
	*d.log = append(*d.log, "denylist:"+sesionID.String())
	return d.err
}

func (d denylistEspia) Revocado(context.Context, uuid.UUID) (bool, error) { return false, d.err }

var _ RevocadorDeSesiones = denylistEspia{}

// filaDeSesionFalsa devuelve la sesión configurada como resultado de QueryRow.
type filaDeSesionFalsa struct{ sesion repositorio.Sesion }

func (f filaDeSesionFalsa) Scan(dest ...any) error { return asignarSesion(dest, f.sesion) }

// filasDeSesionesFalsas devuelve las sesiones configuradas como resultado de Query.
type filasDeSesionesFalsas struct {
	sesiones []repositorio.Sesion
	actual   int
}

func (f *filasDeSesionesFalsas) Close()                                       {}
func (f *filasDeSesionesFalsas) Err() error                                   { return nil }
func (f *filasDeSesionesFalsas) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *filasDeSesionesFalsas) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *filasDeSesionesFalsas) Values() ([]any, error)                       { return nil, nil }
func (f *filasDeSesionesFalsas) RawValues() [][]byte                          { return nil }
func (f *filasDeSesionesFalsas) Conn() *pgx.Conn                              { return nil }

// TypeMap entró en la interfaz pgx.Rows con pgx v5.11; este fake solo compara
// columnas por posición, así que el mapa por defecto alcanza.
func (f *filasDeSesionesFalsas) TypeMap() *pgtype.Map { return pgtype.NewMap() }

func (f *filasDeSesionesFalsas) Next() bool {
	f.actual++
	return f.actual <= len(f.sesiones)
}

func (f *filasDeSesionesFalsas) Scan(dest ...any) error {
	return asignarSesion(dest, f.sesiones[f.actual-1])
}

// asignarSesion copia la sesión a los destinos de Scan en el orden de columnas de auth.sesiones.
func asignarSesion(dest []any, s repositorio.Sesion) error {
	if len(dest) != 11 {
		return fmt.Errorf("destinos de Scan = %d, quiero 11", len(dest))
	}
	*dest[0].(*pgtype.UUID) = s.ID
	*dest[1].(*pgtype.UUID) = s.FamiliaID
	*dest[2].(*pgtype.UUID) = s.ParteID
	*dest[3].(*string) = s.ContextoActivo
	*dest[4].(*string) = s.Dispositivo
	*dest[5].(*[]byte) = s.RefreshHash
	*dest[6].(*pgtype.Timestamptz) = s.CreadaEn
	*dest[7].(*pgtype.Timestamptz) = s.UltimoUsoEn
	*dest[8].(*pgtype.Timestamptz) = s.UsadoEn
	*dest[9].(*pgtype.Timestamptz) = s.RevocadaEn
	*dest[10].(*pgtype.Timestamptz) = s.VenceEn
	return nil
}

// sesionVigenteDePrueba arma una sesión no revocada, no usada y vigente de la parte y la familia.
func sesionVigenteDePrueba(parteID, familiaID uuid.UUID) repositorio.Sesion {
	ahora := time.Now().UTC()
	return repositorio.Sesion{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		FamiliaID:   pgtype.UUID{Bytes: familiaID, Valid: true},
		ParteID:     pgtype.UUID{Bytes: parteID, Valid: true},
		CreadaEn:    pgtype.Timestamptz{Time: ahora, Valid: true},
		UltimoUsoEn: pgtype.Timestamptz{Time: ahora, Valid: true},
		VenceEn:     pgtype.Timestamptz{Time: ahora.Add(time.Hour), Valid: true},
	}
}

// servicioConTransaccionFalsa arma el servicio sobre la transacción falsa y la denylist espía que comparten el log.
func servicioConTransaccionFalsa(tx *fakeTransaccionAuth, denylist RevocadorDeSesiones) *ServicioDeSesion {
	abridor := fakeAbridorAuth{tx: tx}
	return &ServicioDeSesion{
		pool:     abridor,
		sesiones: &GestorSesiones{pool: abridor, eventos: RegistradorNulo{}},
		denylist: denylist,
	}
}

// posicionesDelLog devuelve el índice de la revocación en base, del commit que la confirma y de la primera publicación, o -1.
func posicionesDelLog(log []string) (revocacion, commit, publicacion int) {
	revocacion, commit, publicacion = -1, -1, -1
	for i, entrada := range log {
		switch {
		case revocacion < 0 && strings.Contains(entrada, "UPDATE auth.sesiones") && strings.Contains(entrada, "revocada_en"):
			revocacion = i
		case revocacion >= 0 && commit < 0 && entrada == "commit":
			commit = i
		case publicacion < 0 && strings.HasPrefix(entrada, "denylist:"):
			publicacion = i
		}
	}
	return revocacion, commit, publicacion
}

func TestCerrarTodasLasSesionesRevocaEnBaseAntesDePublicarCadaSid(t *testing.T) {
	t.Parallel()

	parteID, familiaA, familiaB := uuid.New(), uuid.New(), uuid.New()
	var log []string
	tx := &fakeTransaccionAuth{eventos: &log, filas: &filasDeSesionesFalsas{
		sesiones: []repositorio.Sesion{sesionVigenteDePrueba(parteID, familiaA), sesionVigenteDePrueba(parteID, familiaB)},
	}}
	servicio := servicioConTransaccionFalsa(tx, denylistEspia{log: &log})

	if err := servicio.CerrarTodasLasSesiones(t.Context(), parteID); err != nil {
		t.Fatalf("CerrarTodasLasSesiones: %v", err)
	}

	revocacion, commit, publicacion := posicionesDelLog(log)
	if revocacion < 0 || commit < revocacion || publicacion < commit {
		t.Fatalf("orden del log = revocación %d, commit %d, primera publicación %d; quiero revocar, confirmar y publicar: %v", revocacion, commit, publicacion, log)
	}
	for _, familia := range []uuid.UUID{familiaA, familiaB} {
		if !slices.Contains(log, "denylist:"+familia.String()) {
			t.Errorf("la familia %s no se publicó: %v", familia, log)
		}
	}
}

func TestCerrarTodasLasSesionesConDenylistQueFallaDejaTodoRevocadoYSigueIntentando(t *testing.T) {
	t.Parallel()

	parteID, familiaA, familiaB := uuid.New(), uuid.New(), uuid.New()
	var log []string
	tx := &fakeTransaccionAuth{eventos: &log, filas: &filasDeSesionesFalsas{
		sesiones: []repositorio.Sesion{sesionVigenteDePrueba(parteID, familiaA), sesionVigenteDePrueba(parteID, familiaB)},
	}}
	servicio := servicioConTransaccionFalsa(tx, denylistEspia{log: &log, err: ConFailClosed(errors.New("redis caído"))})

	err := servicio.CerrarTodasLasSesiones(t.Context(), parteID)
	if !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("error = %v, quiero %v", err, ErrAuthNoDisponible)
	}

	revocacion, commit, publicacion := posicionesDelLog(log)
	if revocacion < 0 || commit < revocacion || publicacion < commit {
		t.Fatalf("las familias debían quedar revocadas y confirmadas antes del primer intento de publicar: %v", log)
	}
	for _, familia := range []uuid.UUID{familiaA, familiaB} {
		if !slices.Contains(log, "denylist:"+familia.String()) {
			t.Errorf("no se intentó publicar la familia %s tras el fallo: %v", familia, log)
		}
	}
}

func TestCerrarSesionPorRefreshRevocaEnBaseAntesDePublicarElSid(t *testing.T) {
	t.Parallel()

	parteID, familiaID := uuid.New(), uuid.New()
	var log []string
	tx := &fakeTransaccionAuth{eventos: &log, fila: filaDeSesionFalsa{sesion: sesionVigenteDePrueba(parteID, familiaID)}}
	servicio := servicioConTransaccionFalsa(tx, denylistEspia{log: &log})

	if err := servicio.CerrarSesionPorRefresh(t.Context(), parteID, "refresh"); err != nil {
		t.Fatalf("CerrarSesionPorRefresh: %v", err)
	}

	revocacion, commit, publicacion := posicionesDelLog(log)
	if revocacion < 0 || commit < revocacion || publicacion < commit {
		t.Fatalf("orden del log = revocación %d, commit %d, publicación %d; quiero revocar, confirmar y publicar: %v", revocacion, commit, publicacion, log)
	}
	if !slices.Contains(log, "denylist:"+familiaID.String()) {
		t.Errorf("la familia %s no se publicó: %v", familiaID, log)
	}
}

func TestCerrarSesionPorRefreshConDenylistQueFallaResponde503ConLaFamiliaRevocada(t *testing.T) {
	t.Parallel()

	parteID, familiaID := uuid.New(), uuid.New()
	var log []string
	tx := &fakeTransaccionAuth{eventos: &log, fila: filaDeSesionFalsa{sesion: sesionVigenteDePrueba(parteID, familiaID)}}
	servicio := servicioConTransaccionFalsa(tx, denylistEspia{log: &log, err: ConFailClosed(errors.New("redis caído"))})

	err := servicio.CerrarSesionPorRefresh(t.Context(), parteID, "refresh")
	if !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("error = %v, quiero %v", err, ErrAuthNoDisponible)
	}

	revocacion, commit, publicacion := posicionesDelLog(log)
	if revocacion < 0 || commit < revocacion || publicacion < commit {
		t.Fatalf("la familia debía quedar revocada y confirmada antes del intento de publicar: %v", log)
	}
}
