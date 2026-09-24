package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
)

// insertadorDeRegistroFalso registra el propósito, el destino y los efectos que recibe el relleno.
type insertadorDeRegistroFalso struct {
	proposito   Proposito
	celular     string
	correo      string
	origen      string
	adicionales int
	llamadas    int
	err         error
}

func (i *insertadorDeRegistroFalso) InsertarRelleno(_ context.Context, proposito Proposito, celular, correo, origen string, adicionales ...EfectoDeRelleno) (Desafio, error) {
	i.llamadas++
	i.proposito, i.celular, i.correo, i.origen, i.adicionales = proposito, celular, correo, origen, len(adicionales)
	return Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: proposito}, i.err
}

// rellenoDeRegistroDePrueba arma el relleno con el insertador y los contadores falsos.
func rellenoDeRegistroDePrueba(t *testing.T, insertador *insertadorDeRegistroFalso, contadores *contadoresDeRecuperacion) *RellenoDeRegistro {
	t.Helper()

	claves, err := NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	racionador, err := NuevoRacionadorOTP(contadores, claves)
	if err != nil {
		t.Fatalf("NuevoRacionadorOTP: %v", err)
	}
	return nuevoRellenoDeRegistro(insertador, racionador)
}

func TestEsDocumentoRepetido(t *testing.T) {
	t.Parallel()

	violacion := func(restriccion string) error {
		return fmt.Errorf("auth: insertar la persona: %w", &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: restriccion})
	}
	tests := map[string]struct {
		err  error
		want bool
	}{
		"documento repetido":                {err: violacion("personas_documento_uk"), want: true},
		"correo repetido":                   {err: violacion("credenciales_correo_uk"), want: false},
		"celular repetido":                  {err: violacion("credenciales_celular_uk"), want: false},
		"nit repetido":                      {err: violacion("empresas_nit_uk"), want: false},
		"otra violacion sobre el documento": {err: fmt.Errorf("x: %w", &pgconn.PgError{Code: pgerrcode.ForeignKeyViolation, ConstraintName: "personas_documento_uk"}), want: false},
		"error ajeno a postgres":            {err: errors.New("red caida"), want: false},
		"sin error":                         {err: nil, want: false},
	}
	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			if got := esDocumentoRepetido(tc.err); got != tc.want {
				t.Errorf("esDocumentoRepetido(%v) = %v, quiero %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRellenarRegistroConsumeLaMismaRacionQueElAltaReal(t *testing.T) {
	t.Parallel()

	insertador := &insertadorDeRegistroFalso{}
	contadores := &contadoresDeRecuperacion{}
	relleno := rellenoDeRegistroDePrueba(t, insertador, contadores)

	desafio, err := relleno.RellenarRegistro(t.Context(), DocumentoDeRegistro{Tipo: "CC", Numero: "1020304050"}, "+573001112233", "ana@centinela.test", "10.0.0.1")
	if err != nil {
		t.Fatalf("RellenarRegistro: %v", err)
	}

	if desafio.Proposito != PropositoRegistro || desafio.ExpiraEnSegundos != 300 {
		t.Errorf("desafio = %+v, quiero registro/300", desafio)
	}
	if insertador.proposito != PropositoRegistro || insertador.celular != "+573001112233" || insertador.correo != "ana@centinela.test" || insertador.origen != "10.0.0.1" {
		t.Errorf("insertador recibio (%q, %q, %q, %q), quiero (registro, +573001112233, ana@centinela.test, 10.0.0.1): el relleno de registro lleva el celular real", insertador.proposito, insertador.celular, insertador.correo, insertador.origen)
	}
	if insertador.adicionales != 1 {
		t.Errorf("efectos adicionales = %d, quiero 1 (el aviso al dueño)", insertador.adicionales)
	}
	if len(contadores.incrementadas) != 2 {
		t.Errorf("contadores incrementados = %v, quiero el de cuenta y el de origen", contadores.incrementadas)
	}
}

func TestRellenarRegistroSinRacionNoInserta(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		contadores *contadoresDeRecuperacion
		want       error
	}{
		"cuenta bloqueada": {contadores: &contadoresDeRecuperacion{bloqueada: true}, want: ErrDemasiadosEnvios},
		"redis caido":      {contadores: &contadoresDeRecuperacion{falla: errors.New("redis caido")}, want: ErrAuthNoDisponible},
	}
	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			insertador := &insertadorDeRegistroFalso{}
			relleno := rellenoDeRegistroDePrueba(t, insertador, tc.contadores)

			_, err := relleno.RellenarRegistro(t.Context(), DocumentoDeRegistro{Tipo: "CC", Numero: "1"}, "+573001112233", "ana@centinela.test", "10.0.0.1")

			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, quiero %v", err, tc.want)
			}
			if insertador.llamadas != 0 {
				t.Errorf("insertador llamado %d veces, quiero 0 sin racion", insertador.llamadas)
			}
		})
	}
}

func TestEventoDeRellenoPorProposito(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		proposito     Proposito
		wantTipo      string
		wantResultado string
		wantErr       bool
	}{
		"recuperacion":          {proposito: PropositoRecuperacion, wantTipo: TipoEventoRecuperacion, wantResultado: resultadoRecuperacionRelleno},
		"registro":              {proposito: PropositoRegistro, wantTipo: TipoEventoRegistro, wantResultado: resultadoRegistroRelleno},
		"login no se imita":     {proposito: PropositoLogin, wantErr: true},
		"propósito desconocido": {proposito: Proposito("otro"), wantErr: true},
	}
	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			tipo, resultado, err := eventoDeRelleno(tc.proposito)
			if (err != nil) != tc.wantErr {
				t.Fatalf("eventoDeRelleno(%q) error = %v, wantErr %v", tc.proposito, err, tc.wantErr)
			}
			if tipo != tc.wantTipo || resultado != tc.wantResultado {
				t.Errorf("eventoDeRelleno(%q) = (%q, %q), quiero (%q, %q)", tc.proposito, tipo, resultado, tc.wantTipo, tc.wantResultado)
			}
		})
	}
}

func TestHmacDelReenvioDelRellenoNoLoSatisfaceUnCodigo(t *testing.T) {
	t.Parallel()

	pepper := bytes.Repeat([]byte{0x22}, 32)
	relleno := repositorio.DesafioPendientePorIDRow{CelularDestino: celularDeRelleno}
	realPersistido := repositorio.DesafioPendientePorIDRow{
		CelularDestino: "+573001112233",
		ParteID:        pgtype.UUID{Bytes: uuid.New(), Valid: true},
		CredencialID:   pgtype.UUID{Bytes: uuid.New(), Valid: true},
	}

	hmacRelleno, err := hmacDelReenvio(pepper, relleno, "123456")
	if err != nil {
		t.Fatalf("hmacDelReenvio del relleno: %v", err)
	}
	hmacReal, err := hmacDelReenvio(pepper, realPersistido, "123456")
	if err != nil {
		t.Fatalf("hmacDelReenvio real: %v", err)
	}

	if len(hmacRelleno) != 32 || bytes.Equal(hmacRelleno, hmacDeCodigo(pepper, "123456")) {
		t.Error("el HMAC del relleno reenviado lo satisface el codigo 123456, quiero uno aleatorio")
	}
	if !bytes.Equal(hmacReal, hmacDeCodigo(pepper, "123456")) {
		t.Error("el HMAC de un desafio real reenviado debe derivarse del codigo")
	}
}

func TestEsFilaDeRellenoFallaHaciaElLadoSeguro(t *testing.T) {
	t.Parallel()

	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	tests := map[string]struct {
		celular      string
		parteID      pgtype.UUID
		credencialID pgtype.UUID
		want         bool
	}{
		"fila real de registro con ambos ids":              {celular: "+573001112233", parteID: id, credencialID: id, want: false},
		"relleno de registro con celular real y ids nulos": {celular: "+573001112233", want: true},
		"relleno de recuperacion con el centinela":         {celular: celularDeRelleno, want: true},
		"centinela con ambos ids puestos":                  {celular: celularDeRelleno, parteID: id, credencialID: id, want: true},
		"solo la parte nula":                               {celular: "+573001112233", credencialID: id, want: false},
		"solo la credencial nula":                          {celular: "+573001112233", parteID: id, want: false},
	}
	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			if got := esFilaDeRelleno(tc.celular, tc.parteID, tc.credencialID); got != tc.want {
				t.Errorf("esFilaDeRelleno(%q, %v, %v) = %v, quiero %v", tc.celular, tc.parteID.Valid, tc.credencialID.Valid, got, tc.want)
			}
		})
	}
}

func TestHmacDelReenvioDelRellenoDeRegistroConCelularRealNoLoSatisfaceUnCodigo(t *testing.T) {
	t.Parallel()

	pepper := bytes.Repeat([]byte{0x22}, 32)
	relleno := repositorio.DesafioPendientePorIDRow{CelularDestino: "+573001112233"}

	hmacRelleno, err := hmacDelReenvio(pepper, relleno, "123456")
	if err != nil {
		t.Fatalf("hmacDelReenvio: %v", err)
	}

	if len(hmacRelleno) != 32 || bytes.Equal(hmacRelleno, hmacDeCodigo(pepper, "123456")) {
		t.Error("el relleno de registro con celular real quedo con un HMAC que el codigo 123456 satisface")
	}
	if !esRelleno(repositorio.AuthDesafio{CelularDestino: "+573001112233"}) {
		t.Error("esRelleno no reconoce el relleno de registro con celular real: otp/resend le enviaria un OTP real")
	}
}

func TestDespachadorEntregaElAvisoDeRegistroDuplicadoAlCorreoDelDueno(t *testing.T) {
	t.Parallel()

	payload, err := payloadDeAviso(Aviso{
		Tipo:      TipoAvisoRegistroDuplicado,
		Destino:   DestinoCorreo,
		Correo:    "dueno@centinela.test",
		Plantilla: plantillaRegistroDuplicado,
		ParteID:   uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("payloadDeAviso: %v", err)
	}
	correo, whatsapp := &canalCorreoFalso{id: "correo-1"}, &canalOTPFalso{id: "whatsapp-1"}
	almacen := nuevoAlmacenAvisosFalso(avisoReclamado{id: 1, tipo: TipoAvisoRegistroDuplicado, payload: payload})
	despachador := nuevoDespachadorDeAvisos(almacen, NuevoNotificadorDeAvisos(correo, whatsapp))

	if err := despachador.DespacharAvisos(t.Context()); err != nil {
		t.Fatalf("DespacharAvisos: %v", err)
	}

	if !almacen.entregados[1] {
		t.Fatal("el aviso registro_duplicado no quedó entregado: el despachador no lo tiene registrado")
	}
	if correo.ultimoCorreo != "dueno@centinela.test" || whatsapp.ultimoCelular != "" {
		t.Errorf("entrega = (correo %q, whatsapp %q), quiero solo el correo del dueño", correo.ultimoCorreo, whatsapp.ultimoCelular)
	}
	if strings.ContainsAny(correo.ultimoCuerpo, "0123456789") {
		t.Errorf("el cuerpo trae dígitos, posible código o dato: %q", correo.ultimoCuerpo)
	}
	if !strings.Contains(correo.ultimoAsunto, "registro") {
		t.Errorf("asunto = %q, quiero que nombre el intento de registro", correo.ultimoAsunto)
	}
}

// rellenadorDeRegistroFalso devuelve un desafío fijo sin tocar la base.
type rellenadorDeRegistroFalso struct{ desafio Desafio }

func (r rellenadorDeRegistroFalso) RellenarRegistro(context.Context, DocumentoDeRegistro, string, string, string) (Desafio, error) {
	return r.desafio, nil
}

// exigirMismosCamposPoblados exige que ningún campo del relleno esté vacío donde el alta real lo llena, y al revés.
func exigirMismosCamposPoblados(t *testing.T, realPersistido, relleno any) {
	t.Helper()

	vReal, vRelleno := reflect.ValueOf(realPersistido), reflect.ValueOf(relleno)
	for i := range vReal.NumField() {
		nombre := vReal.Type().Field(i).Name
		if vReal.Field(i).IsZero() != vRelleno.Field(i).IsZero() {
			t.Errorf("campo %s: vacío en el alta real = %v, en el relleno = %v; el 201 los distinguiría", nombre, vReal.Field(i).IsZero(), vRelleno.Field(i).IsZero())
		}
	}
}

func TestRegistroDeRellenoPueblaLosMismosCamposQueElAltaReal(t *testing.T) {
	t.Parallel()

	desafio := Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRegistro}
	s := &RegistradorDePersonas{relleno: rellenadorDeRegistroFalso{desafio: desafio}}
	ahora := time.Now().UTC()

	entrada := entradaDeRegistro{correo: "ana@centinela.test", celular: "+573001112233", nombreCompleto: "Ana Centinela", tipoDocumento: "CC", numeroDocumento: "1"}
	relleno, err := s.registroDeRelleno(t.Context(), entrada)
	if err != nil {
		t.Fatalf("registroDeRelleno: %v", err)
	}
	realPersistido := datosPersistidos{parteID: uuid.New(), credencialID: uuid.New(), correo: entrada.correo, celular: entrada.celular, nombreCompleto: entrada.nombreCompleto, creadaEn: ahora}.resultado(desafio)
	exigirMismosCamposPoblados(t, realPersistido, relleno)
	if relleno.ParteID == realPersistido.ParteID || relleno.Desafio != desafio {
		t.Errorf("relleno = %+v, quiero ids propios y el desafío del relleno", relleno)
	}

	entradaEmpresa := entradaDeRegistroEmpresa{razonSocial: "Centinela SAS", nit: "9001234561", correo: "e@centinela.test", celular: "+573001112244", representanteTipoDocumento: "CC", representanteNumeroDocumento: "1"}
	rellenoEmpresa, err := s.registroDeEmpresaDeRelleno(t.Context(), entradaEmpresa)
	if err != nil {
		t.Fatalf("registroDeEmpresaDeRelleno: %v", err)
	}
	realEmpresa := datosEmpresaPersistidos{
		empresaParteID: uuid.New(), representanteParteID: uuid.New(), credencialID: uuid.New(),
		razonSocial: entradaEmpresa.razonSocial, nit: entradaEmpresa.nit, correo: entradaEmpresa.correo, celular: entradaEmpresa.celular, creadaEn: ahora,
	}.resultado(desafio)
	exigirMismosCamposPoblados(t, realEmpresa, rellenoEmpresa)
	if rellenoEmpresa.EmpresaParteID == rellenoEmpresa.RepresentanteParteID {
		t.Error("empresa_id e id del relleno coinciden: deben ser uuid distintos como en el alta real")
	}
}

// contadorDeAvisosFalso cuenta en memoria por clave o falla con el error configurado.
type contadorDeAvisosFalso struct {
	valores map[string]int64
	claves  []string
	err     error
}

func (c *contadorDeAvisosFalso) Incrementar(_ context.Context, clave string, _ time.Duration) (int64, error) {
	if c.err != nil {
		return 0, c.err
	}
	c.valores[clave]++
	c.claves = append(c.claves, clave)
	return c.valores[clave], nil
}

func (*contadorDeAvisosFalso) Bloquear(context.Context, string, time.Duration) error { return nil }

func (*contadorDeAvisosFalso) Bloqueada(context.Context, string) (bool, error) { return false, nil }

func TestPermitirAvisoDeDuplicadoTopaPorDueno(t *testing.T) {
	t.Parallel()

	claves, err := NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	contador := &contadorDeAvisosFalso{valores: map[string]int64{}}
	racionador, err := NuevoRacionadorOTP(contador, claves)
	if err != nil {
		t.Fatalf("NuevoRacionadorOTP: %v", err)
	}

	for intento := 1; intento <= maxAvisosDuplicadoPorDueno; intento++ {
		if !racionador.PermitirAvisoDeDuplicado(t.Context(), "dueno-a") {
			t.Fatalf("intento %d denegado, el tope es %d por dueño", intento, maxAvisosDuplicadoPorDueno)
		}
	}
	if racionador.PermitirAvisoDeDuplicado(t.Context(), "dueno-a") {
		t.Error("el aviso siguiente al tope debe denegarse")
	}
	if !racionador.PermitirAvisoDeDuplicado(t.Context(), "dueno-b") {
		t.Error("el tope es por dueño: otro dueño debe recibir su aviso")
	}
	if got, want := contador.claves[0], PrefijoAvisoDuplicado+claves.ClaveDeParte("dueno-a"); got != want {
		t.Errorf("clave = %q, quiero %q", got, want)
	}
	if strings.Contains(contador.claves[0], "dueno-a") {
		t.Errorf("la clave %q lleva el id de la parte en claro", contador.claves[0])
	}
}

func TestPermitirAvisoDeDuplicadoConRedisCaidoNoAvisaNiFalla(t *testing.T) {
	t.Parallel()

	claves, err := NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	racionador, err := NuevoRacionadorOTP(&contadorDeAvisosFalso{valores: map[string]int64{}, err: errors.New("redis caido")}, claves)
	if err != nil {
		t.Fatalf("NuevoRacionadorOTP: %v", err)
	}

	if racionador.PermitirAvisoDeDuplicado(t.Context(), "dueno-a") {
		t.Error("con Redis caído no debe avisar")
	}
}
