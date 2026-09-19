//go:build integration

package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// nitUnicoDeRegistro genera un NIT colombiano valido y distinto por llamada.
func nitUnicoDeRegistro() string {
	id := uuid.New()
	n := int64(id[0])<<32 | int64(id[1])<<24 | int64(id[2])<<16 | int64(id[3])<<8 | int64(id[4])
	return cuerpoDeNIT(fmt.Sprintf("%09d", n%1000000000))
}

// cuerpoDeNIT agrega el digito de verificacion DIAN al cuerpo recibido.
func cuerpoDeNIT(cuerpo string) string {
	pesos := []int{3, 7, 13, 17, 19, 23, 29, 37, 41, 43, 47, 53, 59, 67, 71}
	suma := 0
	for i := 0; i < len(cuerpo); i++ {
		suma += int(cuerpo[len(cuerpo)-1-i]-'0') * pesos[i]
	}
	digito := suma % 11
	if digito > 1 {
		digito = 11 - digito
	}
	return cuerpo + strconv.Itoa(digito)
}

// solicitudValidaDeRegistroEmpresa arma el alta con los dos consentimientos obligatorios aceptados.
func solicitudValidaDeRegistroEmpresa(nit, correo, celular, documento string) SolicitudDeRegistroEmpresa {
	return SolicitudDeRegistroEmpresa{
		RazonSocial:                  "Centinela SAS",
		NIT:                          nit,
		Correo:                       correo,
		Contrasena:                   "contrasena-de-prueba",
		Celular:                      celular,
		RepresentanteNombre:          "Ana Centinela",
		RepresentanteTipoDocumento:   "CC",
		RepresentanteNumeroDocumento: documento,
		Consentimientos: []SolicitudDeConsentimiento{
			{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: true},
			{Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true},
		},
		Origen:    "127.0.0.1",
		UserAgent: "prueba-integracion",
	}
}

// registradorDeEmpresaConFakes usa desafios y subcuentas falsos para aislar la parte juridica.
func registradorDeEmpresaConFakes(tb testing.TB, pool *pgxpool.Pool, subcuentas AbridorDeSubcuentas) *RegistradorDePersonas {
	tb.Helper()
	registrador, err := NuevoRegistradorDePersonas(pool, derivadorDeRegistro(tb), NuevoRegistradorEventos(pool),
		creadorDesafioFalso{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRegistro}},
		subcuentas)
	if err != nil {
		tb.Fatalf("NuevoRegistradorDePersonas: %v", err)
	}
	return registrador
}

// contarEnAuth cuenta filas como rol_auth con la consulta y los argumentos recibidos.
func contarEnAuth(tb testing.TB, pool *pgxpool.Pool, consulta string, args ...any) int {
	tb.Helper()
	var total int
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, consulta, args...).Scan(&total)
		}); err != nil {
		tb.Fatalf("contar (%s): %v", consulta, err)
	}
	return total
}

// leerContextoDeEventoRegistro devuelve el contexto del evento registro de la parte.
func leerContextoDeEventoRegistro(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) string {
	tb.Helper()
	var contexto string
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT contexto FROM auth.eventos_autenticacion WHERE actor_parte_id = $1 AND tipo = $2`,
				uuidOPgtype(parteID), TipoEventoRegistro).Scan(&contexto)
		}); err != nil {
		tb.Fatalf("leer el contexto del evento: %v", err)
	}
	return contexto
}

func TestRegistroEmpresaCreaParteJuridicaRepresentanteYSubcuentas(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	subcuentas := &abridorDeSubcuentasFalso{}
	registrador := registradorDeEmpresaConFakes(t, pool, subcuentas)

	nit, correo, celular, documento := nitUnicoDeRegistro(), "emp-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString()
	registro, err := registrador.RegistrarEmpresa(t.Context(), solicitudValidaDeRegistroEmpresa(nit, correo, celular, documento))
	if err != nil {
		t.Fatalf("RegistrarEmpresa: %v", err)
	}

	if registro.RazonSocial != "Centinela SAS" || registro.NIT != nit || registro.Estado != EstadoPendienteVerificacion {
		t.Errorf("registro = %+v, quiero empresa pendiente con razón social y NIT", registro)
	}
	if registro.Desafio.ID == uuid.Nil || registro.Desafio.Proposito != PropositoRegistro {
		t.Errorf("desafio = %+v, quiero uno de proposito registro", registro.Desafio)
	}
	if registro.EmpresaParteID == registro.RepresentanteParteID {
		t.Error("la empresa y el representante comparten parte_id")
	}

	estado, phc := leerCredencialDeRegistro(t, pool, registro.RepresentanteParteID)
	if estado != string(EstadoPendienteVerificacion) || !strings.HasPrefix(phc, "$argon2id$v=19$") {
		t.Errorf("credencial = (%q, %q), quiero pendiente con PHC argon2id", estado, phc)
	}
	if contarEnAuth(t, pool, `SELECT count(*) FROM auth.personas WHERE parte_id = $1 AND documento = $2`,
		uuidOPgtype(registro.RepresentanteParteID), documento) != 1 {
		t.Error("no quedo la persona del representante")
	}
	if contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas WHERE parte_id = $1 AND nit = $2`,
		uuidOPgtype(registro.EmpresaParteID), nit) != 1 {
		t.Error("no quedo la parte juridica")
	}
	if contarEnAuth(t, pool, `SELECT count(*) FROM auth.roles_de_parte WHERE parte_id = $1 AND representada_id = $2 AND rol = 'representante_legal' AND revocado_en IS NULL`,
		uuidOPgtype(registro.RepresentanteParteID), uuidOPgtype(registro.EmpresaParteID)) != 1 {
		t.Error("no quedo el rol representante_legal vigente")
	}
	exigirConsentimientosCopiados(t, pool, registro.RepresentanteParteID)
	if contarEventosDeParte(t, pool, registro.RepresentanteParteID, TipoEventoRegistro, resultadoRegistroCreado) != 1 {
		t.Error("no quedo el evento registro/creado")
	}
	if contexto := leerContextoDeEventoRegistro(t, pool, registro.RepresentanteParteID); contexto != contextoRegistroEmpresa {
		t.Errorf("contexto del evento = %q, quiero %q", contexto, contextoRegistroEmpresa)
	}

	llamadas := map[string]bool{}
	for _, id := range subcuentas.llamadas {
		llamadas[id] = true
	}
	if len(subcuentas.llamadas) != 2 || !llamadas[registro.RepresentanteParteID.String()] || !llamadas[registro.EmpresaParteID.String()] {
		t.Errorf("subcuentas abiertas = %v, quiero representante y empresa", subcuentas.llamadas)
	}
}

func TestRegistroEmpresaNormalizaElNITGuardado(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorDeEmpresaConFakes(t, pool, &abridorDeSubcuentasFalso{})

	nit := nitUnicoDeRegistro()
	conSeparadores := nit[:3] + "." + nit[3:6] + "." + nit[6:9] + "-" + nit[9:]
	registro, err := registrador.RegistrarEmpresa(t.Context(),
		solicitudValidaDeRegistroEmpresa(conSeparadores, "nit-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("RegistrarEmpresa: %v", err)
	}
	if registro.NIT != nit {
		t.Fatalf("NIT en respuesta = %q, quiero %q", registro.NIT, nit)
	}
	if contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas WHERE parte_id = $1 AND nit = $2`,
		uuidOPgtype(registro.EmpresaParteID), nit) != 1 {
		t.Errorf("no quedo el NIT sin separadores para %s", nit)
	}
}

func TestRegistroEmpresaConNitRepetidoEsGenerico(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorDeEmpresaConFakes(t, pool, &abridorDeSubcuentasFalso{})
	nit := nitUnicoDeRegistro()

	if _, err := registrador.RegistrarEmpresa(t.Context(),
		solicitudValidaDeRegistroEmpresa(nit, "nit-1-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), "DOC-"+uuid.NewString())); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	correo2 := "nit-2-" + uuid.NewString() + "@centinela.test"
	_, err := registrador.RegistrarEmpresa(t.Context(),
		solicitudValidaDeRegistroEmpresa(nit, correo2, celularUnicoDeRegistro(), "DOC-"+uuid.NewString()))
	exigirConflictoGenerico(t, err)
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas WHERE nit = $1`, nit); total != 1 {
		t.Errorf("empresas con el NIT = %d, quiero 1", total)
	}
	if total := contarCredencialesPorCorreo(t, pool, correo2); total != 0 {
		t.Errorf("credenciales del segundo intento = %d, quiero 0", total)
	}
}

func TestRegistroEmpresaConDocumentoExistenteNoCreaFilas(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorDeEmpresaConFakes(t, pool, &abridorDeSubcuentasFalso{})
	documento := "DOC-" + uuid.NewString()

	if _, err := registrador.RegistrarEmpresa(t.Context(),
		solicitudValidaDeRegistroEmpresa(nitUnicoDeRegistro(), "doc-1-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), documento)); err != nil {
		t.Fatalf("primer registro: %v", err)
	}

	partesAntes := contarEnAuth(t, pool, `SELECT count(*) FROM auth.partes`)
	empresasAntes := contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas`)
	personasAntes := contarEnAuth(t, pool, `SELECT count(*) FROM auth.personas`)
	credencialesAntes := contarEnAuth(t, pool, `SELECT count(*) FROM auth.credenciales`)

	nit2, correo2 := nitUnicoDeRegistro(), "doc-2-"+uuid.NewString()+"@centinela.test"
	_, err := registrador.RegistrarEmpresa(t.Context(),
		solicitudValidaDeRegistroEmpresa(nit2, correo2, celularUnicoDeRegistro(), documento))
	exigirConflictoGenerico(t, err)

	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.partes`); total != partesAntes {
		t.Errorf("partes = %d, quiero %d (H4 no debe crear nada)", total, partesAntes)
	}
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas`); total != empresasAntes {
		t.Errorf("empresas = %d, quiero %d (H4 no debe crear la parte juridica)", total, empresasAntes)
	}
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.personas`); total != personasAntes {
		t.Errorf("personas = %d, quiero %d (H4 no debe crear la persona)", total, personasAntes)
	}
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.credenciales`); total != credencialesAntes {
		t.Errorf("credenciales = %d, quiero %d (H4 no debe crear credencial)", total, credencialesAntes)
	}
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas WHERE nit = $1`, nit2); total != 0 {
		t.Errorf("empresas con el NIT nuevo = %d, quiero 0", total)
	}
}

func TestRegistroEmpresaConcurrenteConElMismoNit(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorDeEmpresaConFakes(t, pool, &abridorDeSubcuentasFalso{})
	nit := nitUnicoDeRegistro()

	resultados := make([]error, 2)
	var wg sync.WaitGroup
	for i := range resultados {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, resultados[i] = registrador.RegistrarEmpresa(context.Background(),
				solicitudValidaDeRegistroEmpresa(nit, fmt.Sprintf("conc-%d-%s@centinela.test", i, uuid.NewString()), celularUnicoDeRegistro(), "DOC-"+uuid.NewString()))
		}(i)
	}
	wg.Wait()

	exitos, repetidos := 0, 0
	for _, err := range resultados {
		switch {
		case err == nil:
			exitos++
		case errors.Is(err, ErrIdentificadorEnUso):
			repetidos++
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if exitos != 1 || repetidos != 1 {
		t.Fatalf("exitos = %d, repetidos = %d; quiero 1 y 1", exitos, repetidos)
	}
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas WHERE nit = $1`, nit); total != 1 {
		t.Errorf("empresas con el NIT = %d, quiero 1", total)
	}
}

func TestCrearEmpresaParaParteVinculaRepresentanteExistente(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	subcuentas := &abridorDeSubcuentasFalso{}
	registrador := registradorDeEmpresaConFakes(t, pool, subcuentas)

	persona, err := registrador.RegistrarPersona(t.Context(),
		solicitudValidaDeRegistro("rep-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro()))
	if err != nil {
		t.Fatalf("RegistrarPersona: %v", err)
	}

	nit := nitUnicoDeRegistro()
	empresaID, err := registrador.CrearEmpresaParaParte(t.Context(), persona.ParteID, "Otra SAS", nit)
	if err != nil {
		t.Fatalf("CrearEmpresaParaParte: %v", err)
	}
	if empresaID == uuid.Nil {
		t.Fatal("CrearEmpresaParaParte devolvio uuid nulo")
	}
	if contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas WHERE parte_id = $1 AND nit = $2 AND razon_social = $3`,
		uuidOPgtype(empresaID), nit, "Otra SAS") != 1 {
		t.Error("no quedo la empresa de la parte existente")
	}
	if contarEnAuth(t, pool, `SELECT count(*) FROM auth.roles_de_parte WHERE parte_id = $1 AND representada_id = $2 AND rol = 'representante_legal' AND revocado_en IS NULL`,
		uuidOPgtype(persona.ParteID), uuidOPgtype(empresaID)) != 1 {
		t.Error("no quedo el rol representante_legal de la parte existente")
	}
	llamadas := map[string]bool{}
	for _, id := range subcuentas.llamadas {
		llamadas[id] = true
	}
	if !llamadas[empresaID.String()] {
		t.Errorf("subcuentas abiertas = %v, quiero la empresa %s", subcuentas.llamadas, empresaID)
	}
}

func TestCrearEmpresaParaParteConNitInvalidoNoCreaNada(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorDeEmpresaConFakes(t, pool, &abridorDeSubcuentasFalso{})
	parteID := uuid.New()

	_, err := registrador.CrearEmpresaParaParte(t.Context(), parteID, "Otra SAS", "9001234569")
	if !errors.Is(err, ErrNitInvalido) {
		t.Fatalf("err = %v, quiero ErrNitInvalido", err)
	}
	if total := contarEnAuth(t, pool, `SELECT count(*) FROM auth.partes WHERE id = $1`, uuidOPgtype(parteID)); total != 0 {
		t.Errorf("partes creadas para el NIT invalido = %d, quiero 0", total)
	}
}
