//go:build integration

package auth

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDispositivosRevocacionYContextosDeLaParte(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)
	empresa, razonSocial := crearRolDeEmpresaDeInicioSesion(t, pool, parteID)

	primera := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	segunda := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	familiaPrimera := familiaDeAccess(t, m, primera.AccessToken)
	familiaSegunda := familiaDeAccess(t, m, segunda.AccessToken)

	sesiones, err := m.sesion.ListarDispositivos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarDispositivos: %v", err)
	}
	if len(sesiones) != 2 {
		t.Fatalf("sesiones activas = %d, quiero 2", len(sesiones))
	}

	if err := m.sesion.RevocarDispositivo(t.Context(), parteID, familiaPrimera); err != nil {
		t.Fatalf("RevocarDispositivo: %v", err)
	}
	sesiones, err = m.sesion.ListarDispositivos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarDispositivos tras revocar: %v", err)
	}
	if len(sesiones) != 1 || sesiones[0].FamiliaID != familiaSegunda {
		t.Fatalf("sesiones tras revocar una = %+v, quiero solo la segunda %s", sesiones, familiaSegunda)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familiaPrimera); err != nil || !revocado {
		t.Fatalf("el sid revocado no se publicó: (%v, %v)", revocado, err)
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), primera.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh de la sesión revocada = %v, quiero ErrRefreshInvalido", err)
	}
	if err := m.sesion.RevocarDispositivo(t.Context(), parteID, familiaPrimera); !errors.Is(err, ErrSesionNoEncontrada) {
		t.Fatalf("segunda revocación = %v, quiero ErrSesionNoEncontrada", err)
	}

	contextos, err := m.sesion.ListarContextos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarContextos: %v", err)
	}
	if len(contextos) != 2 {
		t.Fatalf("contextos = %+v, quiero personal y empresa", contextos)
	}
	if contextos[0].ID != ContextoUsuario || contextos[0].Tipo != ContextoUsuario || contextos[0].Rol != "" {
		t.Errorf("contexto personal = %+v, quiero usuario con rol vacío", contextos[0])
	}
	if contextos[1].ID != ContextoDeEmpresa(empresa) || contextos[1].Nombre != razonSocial || contextos[1].NIT != "nit-"+empresa.String() || contextos[1].Rol != "representante_legal" {
		t.Errorf("contexto de empresa = %+v, quiero %s con nit y rol", contextos[1], empresa)
	}

	if _, err := m.sesion.ActivarContexto(t.Context(), parteID, familiaSegunda, ContextoDeEmpresa(uuid.New())); !errors.Is(err, ErrContextoInvalido) {
		t.Fatalf("contexto ajeno = %v, quiero ErrContextoInvalido", err)
	}

	activo, err := m.sesion.ActivarContexto(t.Context(), parteID, familiaSegunda, ContextoDeEmpresa(empresa))
	if err != nil {
		t.Fatalf("ActivarContexto: %v", err)
	}
	claims := claimsDeAccess(t, m, activo.AccessToken)
	if claims.Contexto != ContextoDeEmpresa(empresa) || claims.Rol != "representante_legal" || claims.ParteID != parteID {
		t.Fatalf("claims del access nuevo = %+v, quiero la empresa %s con rol", claims, empresa)
	}
	if activo.Contexto.NIT != "nit-"+empresa.String() || activo.Contexto.Nombre != razonSocial {
		t.Fatalf("contexto activo = %+v, quiero la empresa %s", activo.Contexto, empresa)
	}

	rotados, err := m.sesion.RotarRefresh(t.Context(), segunda.RefreshToken)
	if err != nil {
		t.Fatalf("RotarRefresh: %v", err)
	}
	claimsRotados := claimsDeAccess(t, m, rotados.AccessToken)
	if claimsRotados.Contexto != ContextoDeEmpresa(empresa) || claimsRotados.Rol != "representante_legal" {
		t.Fatalf("el refresh no conservó el contexto: %+v", claimsRotados)
	}

	if err := m.sesion.CerrarTodasLasSesiones(t.Context(), parteID); err != nil {
		t.Fatalf("CerrarTodasLasSesiones: %v", err)
	}
	sesiones, err = m.sesion.ListarDispositivos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarDispositivos tras logout-all: %v", err)
	}
	if len(sesiones) != 0 {
		t.Fatalf("sesiones tras logout-all = %d, quiero 0", len(sesiones))
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familiaPrimera); err != nil || !revocado {
		t.Fatalf("logout-all no publicó la primera familia: (%v, %v)", revocado, err)
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), rotados.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh tras logout-all = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestCerrarSesionActualPublicaElSidDeLaFamilia(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)

	emitida := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	familia := familiaDeAccess(t, m, emitida.AccessToken)

	if err := m.sesion.CerrarSesionActual(t.Context(), parteID, familia); err != nil {
		t.Fatalf("CerrarSesionActual: %v", err)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familia); err != nil || !revocado {
		t.Fatalf("logout no publicó el sid: (%v, %v)", revocado, err)
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh tras logout = %v, quiero ErrRefreshInvalido", err)
	}
}

// familiaDeAccess resuelve el sid de familia del access emitido.
func familiaDeAccess(tb testing.TB, m montajeDeSesion, access string) uuid.UUID {
	tb.Helper()
	return claimsDeAccess(tb, m, access).SesionID
}

// claimsDeAccess verifica el access con el firmador del montaje.
func claimsDeAccess(tb testing.TB, m montajeDeSesion, access string) ClaimsAccess {
	tb.Helper()
	claims, err := m.tokens.Verificar(access)
	if err != nil {
		tb.Fatalf("Verificar access: %v", err)
	}
	return claims
}
