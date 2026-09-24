//go:build integration

package auth

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// contarRellenosDeRegistro cuenta las filas de relleno de registro: parte y credencial nulas.
func contarRellenosDeRegistro(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	return contarEnAuth(t, pool, `SELECT count(*) FROM auth.desafios WHERE proposito = 'registro' AND parte_id IS NULL AND credencial_id IS NULL`)
}

// duenaActivaDePrueba registra y activa una cuenta cuyo documento sirve de documento existente.
func duenaActivaDePrueba(t *testing.T, pila pilaDeRelleno, prefijo string) SolicitudDeRegistro {
	t.Helper()

	duena := solicitudValidaDeRegistro(prefijo+"-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	alta, err := pila.registrador.RegistrarPersona(t.Context(), duena)
	if err != nil {
		t.Fatalf("alta de la dueña: %v", err)
	}
	activarCredencialDePrueba(t, pila.pool, alta.CredencialID)
	return duena
}

// codigoDeApi devuelve el code del envelope que el cliente vería para el error del servicio.
func codigoDeApi(err error) string {
	if err == nil {
		return "ok"
	}
	var apiErr *platformerrors.APIError
	if errors.As(MapearError(err), &apiErr) {
		return apiErr.Code
	}
	return err.Error()
}

// codigosDeVerifyPhone pide n veces verify-phone con un código errado y devuelve el code de cada respuesta.
func codigosDeVerifyPhone(t *testing.T, activador *ActivadorDeCuenta, celular string, n int) []string {
	t.Helper()

	codigos := make([]string, 0, n)
	for range n {
		activacion, err := activador.ActivarCuenta(t.Context(), celular, "000000")
		if activacion.ParteID != uuid.Nil {
			t.Fatalf("verify-phone con un código errado activó la parte %s", activacion.ParteID)
		}
		codigos = append(codigos, codigoDeApi(err))
	}
	return codigos
}

// altasParaParidad deja un relleno (documento existente) y un alta real (documento nuevo) y devuelve el celular de cada uno.
func altasParaParidad(t *testing.T, pila pilaDeRelleno, duena SolicitudDeRegistro) (celularExistente, celularNuevo string) {
	t.Helper()

	celularExistente, celularNuevo = celularUnicoDeRegistro(), celularUnicoDeRegistro()
	if _, err := pila.registrador.RegistrarPersona(t.Context(), altaConDocumento(duena, "paridad-e-"+uuid.NewString()+"@centinela.test", celularExistente)); err != nil {
		t.Fatalf("alta con documento existente: %v", err)
	}
	if _, err := pila.registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro("paridad-n-"+uuid.NewString()+"@centinela.test", celularNuevo)); err != nil {
		t.Fatalf("alta con documento nuevo: %v", err)
	}
	return celularExistente, celularNuevo
}

// vencerDesafiosDeRegistro deja vencidos los desafíos de registro del celular.
func vencerDesafiosDeRegistro(t *testing.T, pool *pgxpool.Pool, celular string) {
	t.Helper()

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `UPDATE auth.desafios SET expira_en = now() - interval '1 second' WHERE proposito = 'registro' AND celular_destino = $1`, celular)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("vencer los desafíos de %s: %v", celular, err)
	}
}

func TestAltaDelAtacanteConElCelularDeUnRegistroEnVueloDa409YNoTapaSuDesafio(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := duenaActivaDePrueba(t, pila, "duena-sombra")
	legitimo := solicitudValidaDeRegistro("legitimo-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	altaLegitima, err := pila.registrador.RegistrarPersona(t.Context(), legitimo)
	if err != nil {
		t.Fatalf("registro legítimo en vuelo: %v", err)
	}
	rellenosAntes := contarRellenosDeRegistro(t, pila.pool)

	_, err = pila.registrador.RegistrarPersona(t.Context(), altaConDocumento(duena, "atacante-"+uuid.NewString()+"@centinela.test", legitimo.Celular))

	exigirConflictoGenerico(t, err)
	if rellenos := contarRellenosDeRegistro(t, pila.pool); rellenos != rellenosAntes {
		t.Errorf("filas de relleno = %d, quiero %d: el atacante no debe dejar una fila más reciente con el celular de la víctima", rellenos, rellenosAntes)
	}
	activacion, err := activadorDeRelleno(t, pila).ActivarCuenta(t.Context(), legitimo.Celular, "123456")
	if err != nil || activacion.ParteID != altaLegitima.ParteID {
		t.Fatalf("verify-phone del legítimo con su código real = (%+v, %v), quiero la activación de %s", activacion, err, altaLegitima.ParteID)
	}
}

func TestVerifyPhoneEsIgualConDocumentoNuevoOExistente(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := duenaActivaDePrueba(t, pila, "duena-paridad")
	activador := activadorDeRelleno(t, pila)
	celularExistente, celularNuevo := altasParaParidad(t, pila, duena)

	existente := codigosDeVerifyPhone(t, activador, celularExistente, 4)
	nuevo := codigosDeVerifyPhone(t, activador, celularNuevo, 4)

	esperado := []string{"OtpInvalid", "OtpInvalid", "OtpInvalid", "OtpTooManyAttempts"}
	if !slices.Equal(existente, esperado) || !slices.Equal(nuevo, esperado) {
		t.Errorf("secuencia con documento existente = %v, con documento nuevo = %v, quiero %v en ambos", existente, nuevo, esperado)
	}

	vencidoExistente, vencidoNuevo := altasParaParidad(t, pila, duena)
	vencerDesafiosDeRegistro(t, pila.pool, vencidoExistente)
	vencerDesafiosDeRegistro(t, pila.pool, vencidoNuevo)
	for nombre, celular := range map[string]string{"documento existente": vencidoExistente, "documento nuevo": vencidoNuevo} {
		if codigos := codigosDeVerifyPhone(t, activador, celular, 1); codigos[0] != "OtpExpired" {
			t.Errorf("verify-phone vencido con %s = %v, quiero OtpExpired", nombre, codigos)
		}
	}
}

// rastroDeAlta reúne lo que un alta deja: filas de alta, filas de relleno y avisos a la dueña.
type rastroDeAlta struct {
	tablas   conteoDeAuth
	rellenos int
	avisos   int
}

// tomarRastroDeAlta cuenta el rastro actual del alta y de los avisos al correo de la dueña.
func tomarRastroDeAlta(t *testing.T, pila pilaDeRelleno, correoDuena string) rastroDeAlta {
	t.Helper()

	return rastroDeAlta{tablas: contarTablasDeAlta(t, pila.pool), rellenos: contarRellenosDeRegistro(t, pila.pool), avisos: contarAvisos(t, pila.pool, correoDuena)}
}

// exigirConflictoSinRastro exige el 409 genérico y que el alta no deje relleno, filas ni aviso a la dueña.
func exigirConflictoSinRastro(t *testing.T, pila pilaDeRelleno, err error, correoDuena string, antes rastroDeAlta) {
	t.Helper()

	exigirConflictoGenerico(t, err)
	if despues := tomarRastroDeAlta(t, pila, correoDuena); despues != antes {
		t.Errorf("rastro tras el 409 = %+v, quiero el mismo %+v: el 409 no deja relleno, filas ni aviso", despues, antes)
	}
}

func TestPrechecksDePersonaDanElMismoConflictoConDocumentoNuevoOExistente(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := duenaActivaDePrueba(t, pila, "duena-matriz")
	tomada := solicitudValidaDeRegistro("tomada-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	if _, err := pila.registrador.RegistrarPersona(t.Context(), tomada); err != nil {
		t.Fatalf("alta que deja el correo y el celular tomados: %v", err)
	}
	documentos := map[string]func(*SolicitudDeRegistro){
		"documento nuevo": func(*SolicitudDeRegistro) {},
		"documento existente": func(s *SolicitudDeRegistro) {
			s.TipoDocumento, s.NumeroDocumento = duena.TipoDocumento, duena.NumeroDocumento
		},
	}
	tomados := map[string]func(*SolicitudDeRegistro){
		"correo tomado":  func(s *SolicitudDeRegistro) { s.Correo = tomada.Correo },
		"celular tomado": func(s *SolicitudDeRegistro) { s.Celular = tomada.Celular },
	}

	for nombreDocumento, aplicarDocumento := range documentos {
		for nombreTomado, aplicarTomado := range tomados {
			t.Run(nombreDocumento+"/"+nombreTomado, func(t *testing.T) {
				solicitud := solicitudValidaDeRegistro("matriz-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
				aplicarDocumento(&solicitud)
				aplicarTomado(&solicitud)
				antes := tomarRastroDeAlta(t, pila, duena.Correo)

				_, err := pila.registrador.RegistrarPersona(t.Context(), solicitud)

				exigirConflictoSinRastro(t, pila, err, duena.Correo, antes)
			})
		}
	}
}

func TestPrechecksDeEmpresaDanElMismoConflictoConDocumentoNuevoOExistente(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := duenaActivaDePrueba(t, pila, "duena-matriz-emp")
	tomada := solicitudValidaDeRegistroEmpresa(nitUnicoDeRegistro(), "emp-tomada-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString())
	if _, err := pila.registrador.RegistrarEmpresa(t.Context(), tomada); err != nil {
		t.Fatalf("alta que deja el NIT, el correo y el celular tomados: %v", err)
	}
	documentos := map[string]func(*SolicitudDeRegistroEmpresa){
		"documento nuevo": func(*SolicitudDeRegistroEmpresa) {},
		"documento existente": func(s *SolicitudDeRegistroEmpresa) {
			s.RepresentanteTipoDocumento, s.RepresentanteNumeroDocumento = duena.TipoDocumento, duena.NumeroDocumento
		},
	}
	tomados := map[string]func(*SolicitudDeRegistroEmpresa){
		"correo tomado":  func(s *SolicitudDeRegistroEmpresa) { s.Correo = tomada.Correo },
		"celular tomado": func(s *SolicitudDeRegistroEmpresa) { s.Celular = tomada.Celular },
		"NIT tomado":     func(s *SolicitudDeRegistroEmpresa) { s.NIT = tomada.NIT },
	}

	for nombreDocumento, aplicarDocumento := range documentos {
		for nombreTomado, aplicarTomado := range tomados {
			t.Run(nombreDocumento+"/"+nombreTomado, func(t *testing.T) {
				solicitud := solicitudValidaDeRegistroEmpresa(nitUnicoDeRegistro(), "emp-matriz-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString())
				aplicarDocumento(&solicitud)
				aplicarTomado(&solicitud)
				antes := tomarRastroDeAlta(t, pila, duena.Correo)

				_, err := pila.registrador.RegistrarEmpresa(t.Context(), solicitud)

				exigirConflictoSinRastro(t, pila, err, duena.Correo, antes)
			})
		}
	}
}

func TestNitTomadoNoSeLiberaAlCerrarLaCredencialDelRepresentante(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	tomada := solicitudValidaDeRegistroEmpresa(nitUnicoDeRegistro(), "emp-cerrada-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString())
	alta, err := pila.registrador.RegistrarEmpresa(t.Context(), tomada)
	if err != nil {
		t.Fatalf("alta de la empresa: %v", err)
	}
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pila.pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `UPDATE auth.credenciales SET estado = 'cerrado' WHERE id = $1`, uuidOPgtype(alta.CredencialID))
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("cerrar la credencial del representante: %v", err)
	}

	solicitud := solicitudValidaDeRegistroEmpresa(tomada.NIT, "emp-nit-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString())
	_, err = pila.registrador.RegistrarEmpresa(t.Context(), solicitud)

	exigirConflictoGenerico(t, err)
}

func TestReenvioDelRellenoDeRecuperacionDejaHmacAleatorioYNoEnvia(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	claves, err := NuevasClaves(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	relleno := &rellenoPostgres{abridor: poolDeAuth{pool: pila.pool}, eventos: NuevoRegistradorEventos(pila.pool), claves: claves, pepper: pepperDeDesafio}
	desafio, err := relleno.InsertarRelleno(t.Context(), PropositoRecuperacion, celularDeRelleno, "nadie-"+uuid.NewString()+"@centinela.test", "127.0.0.1")
	if err != nil {
		t.Fatalf("InsertarRelleno de recuperación: %v", err)
	}
	envejecerRellenoDePrueba(t, pila.pool, desafio.ID)
	enviosAntes := len(pila.canal.envios)

	reenviado, err := pila.gestor.ReenviarDesafio(t.Context(), desafio.ID, "127.0.0.1")

	if err != nil || reenviado.ID != desafio.ID || reenviado.Proposito != PropositoRecuperacion {
		t.Fatalf("otp/resend sobre el relleno de recuperación = (%+v, %v), quiero el mismo desafío sin error", reenviado, err)
	}
	if len(pila.canal.envios) != enviosAntes {
		t.Errorf("envíos nuevos = %v, quiero ninguno", pila.canal.envios[enviosAntes:])
	}
	fila := leerFilaDeRelleno(t, pila.pool, desafio.ID)
	if len(fila.hmac) != 32 || bytes.Equal(fila.hmac, hmacDeCodigo(pepperDeDesafio, "123456")) {
		t.Error("tras el reenvío el relleno de recuperación quedó con un HMAC que el código 123456 satisface")
	}
	if fila.celular != celularDeRelleno || fila.parteID.Valid || fila.credencialID.Valid {
		t.Errorf("fila = %+v, quiero el centinela %q con parte y credencial nulas", fila, celularDeRelleno)
	}
}
