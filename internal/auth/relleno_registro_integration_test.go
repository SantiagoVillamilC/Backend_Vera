//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// canalEspiaDeEnvios cuenta cada envío al celular, con el código fijo del simulador.
type canalEspiaDeEnvios struct {
	canalConFijo
	envios []string
}

func (c *canalEspiaDeEnvios) Enviar(ctx context.Context, celular, codigo string) (string, error) {
	c.envios = append(c.envios, celular)
	return c.canalConFijo.Enviar(ctx, celular, codigo)
}

// notificadorDeRelleno guarda cada aviso que el despachador entrega.
type notificadorDeRelleno struct{ entregados []Aviso }

func (n *notificadorDeRelleno) Enviar(_ context.Context, aviso Aviso) (string, error) {
	n.entregados = append(n.entregados, aviso)
	return "relleno-" + aviso.Correo, nil
}

// pilaDeRelleno reúne el registrador real con relleno, el canal espía y el gestor de desafíos.
type pilaDeRelleno struct {
	pool        *pgxpool.Pool
	registrador *RegistradorDePersonas
	gestor      *GestorDesafios
	canal       *canalEspiaDeEnvios
}

// pilaDeRellenoDePrueba arma el registrador con el gestor real, el relleno real y racionamiento en memoria.
func pilaDeRellenoDePrueba(t *testing.T) pilaDeRelleno {
	t.Helper()

	pool := abrirPoolDeAuth(t)
	canal := &canalEspiaDeEnvios{canalConFijo: canalConFijo{codigo: "123456"}}
	gestor := gestorDeRegistro(t, pool, canal)
	claves, err := NuevasClaves(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	relleno, err := NuevoRellenoDeRegistro(pool, NuevoRegistradorEventos(pool), &contadoresDeRegistro{valores: map[string]int64{}}, claves, pepperDeDesafio)
	if err != nil {
		t.Fatalf("NuevoRellenoDeRegistro: %v", err)
	}
	registrador, err := NuevoRegistradorDePersonas(pool, derivadorDeRegistro(t), NuevoRegistradorEventos(pool), gestor, &abridorDeSubcuentasFalso{}, ConRellenoDeRegistro(relleno))
	if err != nil {
		t.Fatalf("NuevoRegistradorDePersonas: %v", err)
	}
	return pilaDeRelleno{pool: pool, registrador: registrador, gestor: gestor, canal: canal}
}

// activarCredencialDePrueba deja la credencial activa con el celular verificado, como tras verify-phone.
func activarCredencialDePrueba(t *testing.T, pool *pgxpool.Pool, credencialID uuid.UUID) {
	t.Helper()

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `UPDATE auth.credenciales SET estado = 'activo', celular_verificado_en = now() WHERE id = $1`, uuidOPgtype(credencialID))
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("activar la credencial %s: %v", credencialID, err)
	}
}

// conteoDeAuth es el número de filas de las tres tablas que un alta real escribiría.
type conteoDeAuth struct{ partes, personas, credenciales, empresas int }

// contarTablasDeAlta cuenta las filas globales de partes, personas, credenciales y empresas.
func contarTablasDeAlta(t *testing.T, pool *pgxpool.Pool) conteoDeAuth {
	t.Helper()

	return conteoDeAuth{
		partes:       contarEnAuth(t, pool, `SELECT count(*) FROM auth.partes`),
		personas:     contarEnAuth(t, pool, `SELECT count(*) FROM auth.personas`),
		credenciales: contarEnAuth(t, pool, `SELECT count(*) FROM auth.credenciales`),
		empresas:     contarEnAuth(t, pool, `SELECT count(*) FROM auth.empresas`),
	}
}

// filaDeRellenoLeida es lo que el test lee del desafío de relleno.
type filaDeRellenoLeida struct {
	parteID      pgtype.UUID
	credencialID pgtype.UUID
	proposito    string
	celular      string
	estado       string
	vigencia     time.Duration
	hmac         []byte
}

// leerFilaDeRelleno lee como rol_auth la fila del desafío de relleno.
func leerFilaDeRelleno(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) filaDeRellenoLeida {
	t.Helper()

	var fila filaDeRellenoLeida
	var creado, expira time.Time
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT parte_id, credencial_id, proposito, celular_destino, estado, creado_en, expira_en, codigo_hmac FROM auth.desafios WHERE id = $1`,
				uuidOPgtype(id)).Scan(&fila.parteID, &fila.credencialID, &fila.proposito, &fila.celular, &fila.estado, &creado, &expira, &fila.hmac)
		}); err != nil {
		t.Fatalf("leer el desafío de relleno %s: %v", id, err)
	}
	fila.vigencia = expira.Sub(creado)
	return fila
}

// leerAvisoDeRegistroDuplicado devuelve el tipo y el payload de la última fila del outbox con ese correo.
func leerAvisoDeRegistroDuplicado(t *testing.T, pool *pgxpool.Pool, correo string) (string, map[string]any) {
	t.Helper()

	var tipo string
	var crudo []byte
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT tipo, payload FROM auth.outbox WHERE payload->>'correo' = $1 ORDER BY id DESC LIMIT 1`, correo).Scan(&tipo, &crudo)
		}); err != nil {
		t.Fatalf("leer el aviso del outbox para %q: %v", correo, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(crudo, &payload); err != nil {
		t.Fatalf("decodificar el payload %q: %v", crudo, err)
	}
	return tipo, payload
}

// altaConDocumento arma una segunda alta de persona que repite el documento de la primera con otros datos.
func altaConDocumento(base SolicitudDeRegistro, correo, celular string) SolicitudDeRegistro {
	repetida := solicitudValidaDeRegistro(correo, celular)
	repetida.TipoDocumento, repetida.NumeroDocumento = base.TipoDocumento, base.NumeroDocumento
	repetida.NombreCompleto = "Persona Distinta"
	return repetida
}

func TestRegistroConDocumentoRepetidoRellenaSinCrearNiEnviar(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := solicitudValidaDeRegistro("duena-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	altaDuena, err := pila.registrador.RegistrarPersona(t.Context(), duena)
	if err != nil {
		t.Fatalf("alta de la dueña: %v", err)
	}
	activarCredencialDePrueba(t, pila.pool, altaDuena.CredencialID)
	enviosAntes, tablasAntes := len(pila.canal.envios), contarTablasDeAlta(t, pila.pool)

	intruso := altaConDocumento(duena, "intruso-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	registro, err := pila.registrador.RegistrarPersona(t.Context(), intruso)

	if err != nil {
		t.Fatalf("el documento repetido debe responder como un alta nueva, no %v", err)
	}
	if registro.Estado != EstadoPendienteVerificacion || registro.Correo != intruso.Correo || registro.Celular != intruso.Celular || registro.NombreCompleto != intruso.NombreCompleto {
		t.Errorf("registro = %+v, quiero el eco pendiente de lo solicitado", registro)
	}
	if registro.ParteID == uuid.Nil || registro.ParteID == altaDuena.ParteID || registro.Desafio.ID == uuid.Nil || registro.Desafio.Proposito != PropositoRegistro || registro.Desafio.ExpiraEnSegundos != 300 {
		t.Errorf("registro = %+v, quiero ids aleatorios ajenos a la dueña y un desafío registro/300", registro)
	}
	if tablas := contarTablasDeAlta(t, pila.pool); tablas != tablasAntes {
		t.Errorf("filas tras el documento repetido = %+v, quiero las mismas %+v: no debe quedar parte, persona ni credencial", tablas, tablasAntes)
	}
	if total := contarCredencialesPorCorreo(t, pila.pool, intruso.Correo); total != 0 {
		t.Errorf("credenciales del intruso = %d, quiero 0", total)
	}
	if len(pila.canal.envios) != enviosAntes {
		t.Errorf("envios por el canal = %v, quiero ninguno nuevo", pila.canal.envios[enviosAntes:])
	}
	exigirFilaDeRelleno(t, pila.pool, registro.Desafio.ID, intruso.Celular)
	if total := contarEventosDeRellenoDeRegistro(t, pila.pool); total == 0 {
		t.Error("no quedó el evento registro/relleno")
	}
}

// exigirFilaDeRelleno exige parte y credencial nulas, registro, el celular real de la solicitud, 5 minutos y HMAC de 32 bytes.
func exigirFilaDeRelleno(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, celular string) {
	t.Helper()

	fila := leerFilaDeRelleno(t, pool, id)
	if fila.parteID.Valid || fila.credencialID.Valid {
		t.Errorf("parte_id/credencial_id = %v/%v, quiero NULL/NULL", fila.parteID, fila.credencialID)
	}
	if fila.proposito != string(PropositoRegistro) || fila.celular != celular || fila.estado != "pendiente" {
		t.Errorf("proposito/celular/estado = %q/%q/%q, quiero registro/%s/pendiente: el relleno de registro lleva el celular real", fila.proposito, fila.celular, fila.estado, celular)
	}
	if fila.vigencia != 5*time.Minute || len(fila.hmac) != 32 {
		t.Errorf("vigencia/hmac = %s/%d bytes, quiero 5m/32", fila.vigencia, len(fila.hmac))
	}
	if bytes.Equal(fila.hmac, hmacDeCodigo(pepperDeDesafio, "123456")) {
		t.Error("el HMAC del relleno lo satisface el codigo 123456")
	}
}

// contarEventosDeRellenoDeRegistro cuenta los eventos registro/relleno del trail.
func contarEventosDeRellenoDeRegistro(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	return contarEnAuth(t, pool, `SELECT count(*) FROM auth.eventos_autenticacion WHERE tipo = $1 AND resultado = $2`, TipoEventoRegistro, resultadoRegistroRelleno)
}

func TestRegistroConDocumentoRepetidoEncolaElAvisoDelDuenoYElDespachadorLoEntrega(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := solicitudValidaDeRegistro("duena-aviso-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	altaDuena, err := pila.registrador.RegistrarPersona(t.Context(), duena)
	if err != nil {
		t.Fatalf("alta de la dueña: %v", err)
	}
	activarCredencialDePrueba(t, pila.pool, altaDuena.CredencialID)
	if total := contarAvisos(t, pila.pool, duena.Correo); total != 0 {
		t.Fatalf("avisos de la dueña antes del intento = %d, quiero 0", total)
	}

	intruso := altaConDocumento(duena, "intruso-aviso-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	if _, err := pila.registrador.RegistrarPersona(t.Context(), intruso); err != nil {
		t.Fatalf("RegistrarPersona con documento repetido: %v", err)
	}

	if total := contarAvisos(t, pila.pool, duena.Correo); total != 1 {
		t.Fatalf("avisos de la dueña = %d, quiero 1", total)
	}
	tipo, payload := leerAvisoDeRegistroDuplicado(t, pila.pool, duena.Correo)
	if tipo != TipoAvisoRegistroDuplicado || payload["destino"] != DestinoCorreo || payload["parte_id"] != altaDuena.ParteID.String() {
		t.Errorf("aviso = (%q, %v), quiero registro_duplicado por correo con el parte_id de la dueña", tipo, payload)
	}
	exigirPayloadSinDatosDelIntento(t, payload, intruso)
	entregado := despacharHastaEntregar(t, pila.pool, duena.Correo)
	if entregado.Tipo != TipoAvisoRegistroDuplicado || entregado.Correo != duena.Correo || entregado.Destino != DestinoCorreo {
		t.Errorf("aviso entregado = %+v, quiero registro_duplicado al correo de la dueña", entregado)
	}
}

// exigirPayloadSinDatosDelIntento exige que el aviso no lleve ningún dato del intento ni un código.
func exigirPayloadSinDatosDelIntento(t *testing.T, payload map[string]any, intruso SolicitudDeRegistro) {
	t.Helper()

	permitidas := map[string]bool{"destino": true, "correo": true, "plantilla": true, "parte_id": true}
	crudo, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("serializar el payload: %v", err)
	}
	for clave := range payload {
		if !permitidas[clave] {
			t.Errorf("el payload trae la clave %q fuera de destino/correo/plantilla/parte_id", clave)
		}
	}
	for _, dato := range []string{intruso.Correo, intruso.Celular, intruso.NumeroDocumento, intruso.NombreCompleto, "123456"} {
		if bytes.Contains(crudo, []byte(dato)) {
			t.Errorf("el payload %s revela %q del intento", crudo, dato)
		}
	}
}

// despacharHastaEntregar corre el despachador real con un notificador falso hasta que el aviso del correo se entrega.
func despacharHastaEntregar(t *testing.T, pool *pgxpool.Pool, correo string) Aviso {
	t.Helper()

	notificador := &notificadorDeRelleno{}
	despachador := nuevoDespachadorDeAvisos(&almacenPostgres{pool: pool, politica: PoliticaReintentoPorDefecto()}, notificador)
	for range 20 {
		_ = despachador.DespacharAvisos(t.Context())
		if leerAviso(t, pool, correo).despachadoEn.Valid {
			break
		}
	}
	if !leerAviso(t, pool, correo).despachadoEn.Valid {
		t.Fatalf("el despachador no entregó el aviso de %q", correo)
	}
	for _, aviso := range notificador.entregados {
		if aviso.Correo == correo {
			return aviso
		}
	}
	t.Fatalf("el notificador no recibió el aviso de %q entre %d entregas", correo, len(notificador.entregados))
	return Aviso{}
}

func TestRegistroConDocumentoRepetidoSinCanalVerificadoNoAvisa(t *testing.T) {
	casos := map[string]func(t *testing.T, pila pilaDeRelleno, alta Registro){
		"credencial pendiente sin celular verificado": func(*testing.T, pilaDeRelleno, Registro) {},
		"credencial cerrada": func(t *testing.T, pila pilaDeRelleno, alta Registro) {
			t.Helper()
			if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pila.pool},
				func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
					_, err := tx.Exec(ctx, `UPDATE auth.credenciales SET estado = 'cerrado', celular_verificado_en = now() WHERE id = $1`, uuidOPgtype(alta.CredencialID))
					return struct{}{}, err
				}); err != nil {
				t.Fatalf("cerrar la credencial: %v", err)
			}
		},
	}
	for nombre, preparar := range casos {
		t.Run(nombre, func(t *testing.T) {
			pila := pilaDeRellenoDePrueba(t)
			dueno := solicitudValidaDeRegistro("dueno-sin-canal-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
			alta, err := pila.registrador.RegistrarPersona(t.Context(), dueno)
			if err != nil {
				t.Fatalf("alta del dueño: %v", err)
			}
			preparar(t, pila, alta)
			tablasAntes := contarTablasDeAlta(t, pila.pool)

			reintento := altaConDocumento(dueno, "reintento-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
			registro, err := pila.registrador.RegistrarPersona(t.Context(), reintento)

			if err != nil {
				t.Fatalf("sin credencial con canal verificado debe rellenar igual, sin error ni 5xx: %v", err)
			}
			if registro.Desafio.ID == uuid.Nil || registro.Estado != EstadoPendienteVerificacion {
				t.Errorf("registro = %+v, quiero el 201 de relleno", registro)
			}
			if total := contarAvisos(t, pila.pool, dueno.Correo); total != 0 {
				t.Errorf("avisos encolados = %d, quiero 0: no hay canal verificado al que avisar", total)
			}
			if tablas := contarTablasDeAlta(t, pila.pool); tablas != tablasAntes {
				t.Errorf("filas tras el relleno = %+v, quiero las mismas %+v", tablas, tablasAntes)
			}
			exigirFilaDeRelleno(t, pila.pool, registro.Desafio.ID, reintento.Celular)
		})
	}
}

func TestRegistroDeRellenoNoSeActivaNiSeReenviaConCodigoReal(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := solicitudValidaDeRegistro("duena-cadena-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	altaDuena, err := pila.registrador.RegistrarPersona(t.Context(), duena)
	if err != nil {
		t.Fatalf("alta de la dueña: %v", err)
	}
	activarCredencialDePrueba(t, pila.pool, altaDuena.CredencialID)
	intruso := altaConDocumento(duena, "intruso-cadena-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	registro, err := pila.registrador.RegistrarPersona(t.Context(), intruso)
	if err != nil {
		t.Fatalf("RegistrarPersona con documento repetido: %v", err)
	}
	enviosAntes := len(pila.canal.envios)

	envejecerRellenoDePrueba(t, pila.pool, registro.Desafio.ID)
	reenviado, err := pila.gestor.ReenviarDesafio(t.Context(), registro.Desafio.ID, "127.0.0.1")

	if err != nil {
		t.Fatalf("otp/resend sobre el relleno debe responder 200 sin error: %v", err)
	}
	if reenviado.ID != registro.Desafio.ID || reenviado.Proposito != PropositoRegistro || len(pila.canal.envios) != enviosAntes {
		t.Errorf("reenvío = %+v con %d envíos nuevos, quiero el mismo desafío registro y ninguno", reenviado, len(pila.canal.envios)-enviosAntes)
	}
	if fila := leerFilaDeRelleno(t, pila.pool, registro.Desafio.ID); len(fila.hmac) != 32 || bytes.Equal(fila.hmac, hmacDeCodigo(pepperDeDesafio, "123456")) {
		t.Error("tras el reenvío el relleno quedó con un HMAC que el código 123456 satisface (P-50)")
	}
	exigirQueVerifyPhoneNoActivaNada(t, pila, intruso)
}

// activadorDeRelleno arma el activador real sobre el pool y el gestor de la pila.
func activadorDeRelleno(t *testing.T, pila pilaDeRelleno) *ActivadorDeCuenta {
	t.Helper()

	activador, err := NuevoActivadorDeCuenta(pila.pool, pila.gestor, NuevoGestorDeEstados(NuevoRegistradorEventos(pila.pool)), pepperDeDesafio)
	if err != nil {
		t.Fatalf("NuevoActivadorDeCuenta: %v", err)
	}
	return activador
}

// exigirQueVerifyPhoneNoActivaNada exige OtpInvalid con el celular del intruso y ChallengeInvalid con el centinela, sin activar nada.
func exigirQueVerifyPhoneNoActivaNada(t *testing.T, pila pilaDeRelleno, intruso SolicitudDeRegistro) {
	t.Helper()

	activador := activadorDeRelleno(t, pila)
	activacion, err := activador.ActivarCuenta(t.Context(), intruso.Celular, "123456")
	if !errors.Is(err, ErrCodigoInvalido) || activacion.ParteID != uuid.Nil {
		t.Errorf("verify-phone con el celular del intruso = (%+v, %v), quiero OtpInvalid sin activar, como un alta real con el código errado", activacion, err)
	}
	activacion, err = activador.ActivarCuenta(t.Context(), celularDeRelleno, "123456")
	if !errors.Is(err, ErrDesafioInvalido) || activacion.ParteID != uuid.Nil {
		t.Errorf("verify-phone con el centinela = (%+v, %v), quiero ChallengeInvalid sin activar: ninguna fila de registro lleva el centinela", activacion, err)
	}
	if total := contarCredencialesPorCorreo(t, pila.pool, intruso.Correo); total != 0 {
		t.Errorf("credenciales del intruso tras verify-phone = %d, quiero 0", total)
	}
}

// envejecerRellenoDePrueba corre el último envío fuera de la ventana de enfriamiento del reenvío.
func envejecerRellenoDePrueba(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `UPDATE auth.desafios SET ultimo_envio_en = now() - interval '61 seconds' WHERE id = $1`, uuidOPgtype(id))
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("envejecer el desafío %s: %v", id, err)
	}
}

func TestRegistroConAltaCompensadaYDocumentoRepetidoRellenaSinAvisar(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	causa := fmt.Errorf("%w: canal caido", ErrEntregaFallida)
	fallido, err := NuevoRegistradorDePersonas(pila.pool, derivadorDeRegistro(t), NuevoRegistradorEventos(pila.pool),
		creadorDesafioFalso{err: causa}, &abridorDeSubcuentasFalso{})
	if err != nil {
		t.Fatalf("NuevoRegistradorDePersonas: %v", err)
	}
	original := solicitudValidaDeRegistro("compensada-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	if _, err := fallido.RegistrarPersona(t.Context(), original); !errors.Is(err, causa) {
		t.Fatalf("el alta debía fallar por el canal: %v", err)
	}
	exigirAltaCompensada(t, pila.pool, original.Correo)

	// P-48: el reintento idéntico ya no responde 409, responde el 201 de relleno y no queda cuenta.
	registro, err := pila.registrador.RegistrarPersona(t.Context(), original)

	if err != nil {
		t.Fatalf("el reintento de un alta compensada no debe fallar ni entrar en pánico: %v", err)
	}
	if registro.Desafio.ID == uuid.Nil || len(pila.canal.envios) != 0 {
		t.Errorf("registro = %+v con envíos %v, quiero el relleno sin envío", registro, pila.canal.envios)
	}
	if total := contarAvisos(t, pila.pool, original.Correo); total != 0 {
		t.Errorf("avisos = %d, quiero 0: la cuenta compensada no tiene canal verificado", total)
	}
	exigirFilaDeRelleno(t, pila.pool, registro.Desafio.ID, original.Celular)
}

func TestRegistroEmpresaConDocumentoDelRepresentanteRepetidoRellenaSinCrear(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := solicitudValidaDeRegistro("duena-emp-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	altaDuena, err := pila.registrador.RegistrarPersona(t.Context(), duena)
	if err != nil {
		t.Fatalf("alta de la dueña: %v", err)
	}
	activarCredencialDePrueba(t, pila.pool, altaDuena.CredencialID)
	enviosAntes, tablasAntes := len(pila.canal.envios), contarTablasDeAlta(t, pila.pool)

	nit, correo, celular := nitUnicoDeRegistro(), "empresa-rep-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro()
	solicitud := solicitudValidaDeRegistroEmpresa(nit, correo, celular, duena.NumeroDocumento)
	registro, err := pila.registrador.RegistrarEmpresa(t.Context(), solicitud)

	if err != nil {
		t.Fatalf("el documento repetido del representante debe responder como un alta nueva: %v", err)
	}
	if registro.EmpresaParteID == uuid.Nil || registro.RepresentanteParteID == uuid.Nil || registro.EmpresaParteID == registro.RepresentanteParteID || registro.RepresentanteParteID == altaDuena.ParteID {
		t.Errorf("registro = %+v, quiero empresa_id e id aleatorios, distintos entre sí y ajenos a la dueña", registro)
	}
	if registro.RazonSocial != solicitud.RazonSocial || registro.NIT != nit || registro.Correo != correo || registro.Estado != EstadoPendienteVerificacion || registro.Desafio.Proposito != PropositoRegistro {
		t.Errorf("registro = %+v, quiero el eco pendiente de la solicitud", registro)
	}
	if tablas := contarTablasDeAlta(t, pila.pool); tablas != tablasAntes {
		t.Errorf("filas tras el relleno de empresa = %+v, quiero las mismas %+v", tablas, tablasAntes)
	}
	if total := contarEnAuth(t, pila.pool, `SELECT count(*) FROM auth.empresas WHERE nit = $1`, nit); total != 0 {
		t.Errorf("empresas con el NIT = %d, quiero 0", total)
	}
	if len(pila.canal.envios) != enviosAntes {
		t.Errorf("envíos nuevos = %v, quiero ninguno", pila.canal.envios[enviosAntes:])
	}
	if total := contarAvisos(t, pila.pool, duena.Correo); total != 1 {
		t.Errorf("avisos de la dueña = %d, quiero 1", total)
	}
	exigirFilaDeRelleno(t, pila.pool, registro.Desafio.ID, celular)
}

func TestRegistroConCorreoRepetidoYDocumentoNuevoSigueSiendo409ConRelleno(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	correo := "correo-dup-" + uuid.NewString() + "@centinela.test"
	if _, err := pila.registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro(correo, celularUnicoDeRegistro())); err != nil {
		t.Fatalf("primer registro: %v", err)
	}

	_, err := pila.registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro(correo, celularUnicoDeRegistro()))

	exigirConflictoGenerico(t, err)
}

func TestRegistroConCorreoYDocumentoRepetidosDaConflictoGenerico(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	original := solicitudValidaDeRegistro("ambos-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	if _, err := pila.registrador.RegistrarPersona(t.Context(), original); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	rellenosAntes := contarRellenosDeRegistro(t, pila.pool)

	_, err := pila.registrador.RegistrarPersona(t.Context(), original)

	exigirConflictoGenerico(t, err)
	if total := contarCredencialesPorCorreo(t, pila.pool, original.Correo); total != 1 {
		t.Errorf("credenciales con el correo = %d, quiero 1", total)
	}
	if rellenos := contarRellenosDeRegistro(t, pila.pool); rellenos != rellenosAntes {
		t.Errorf("filas de relleno = %d, quiero %d: el 409 no deja relleno", rellenos, rellenosAntes)
	}
}

func TestRegistroConVersionDeConsentimientoInexistenteEsIgualConDocumentoNuevoOExistente(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	existente := solicitudValidaDeRegistro("consent-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	if _, err := pila.registrador.RegistrarPersona(t.Context(), existente); err != nil {
		t.Fatalf("alta del documento existente: %v", err)
	}
	conVersionRota := func(base SolicitudDeRegistro) SolicitudDeRegistro {
		base.Correo, base.Celular = "consent-b-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro()
		base.Consentimientos = []SolicitudDeConsentimiento{
			{Tipo: TipoConsentimientoTratamiento, VersionTexto: "zzz", Aceptado: true},
			{Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true},
		}
		return base
	}
	nuevo := conVersionRota(solicitudValidaDeRegistro("", ""))
	repetido := conVersionRota(existente)

	_, errNuevo := pila.registrador.RegistrarPersona(t.Context(), nuevo)
	_, errRepetido := pila.registrador.RegistrarPersona(t.Context(), repetido)

	if !errors.Is(errNuevo, ErrConsentimientoRequerido) || !errors.Is(errRepetido, ErrConsentimientoRequerido) {
		t.Fatalf("errores = (%v, %v), quiero ErrConsentimientoRequerido en ambos: el documento existente no debe saltarse la validación", errNuevo, errRepetido)
	}
	solicitudEmpresa := solicitudValidaDeRegistroEmpresa(nitUnicoDeRegistro(), "consent-e-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro(), existente.NumeroDocumento)
	solicitudEmpresa.Consentimientos = repetido.Consentimientos
	if _, err := pila.registrador.RegistrarEmpresa(t.Context(), solicitudEmpresa); !errors.Is(err, ErrConsentimientoRequerido) {
		t.Errorf("empresa con documento existente y versión inexistente: err = %v, quiero ErrConsentimientoRequerido", err)
	}
}

func TestRegistroConDocumentoRepetidoAvisaAlDuenoHastaSuTope(t *testing.T) {
	pila := pilaDeRellenoDePrueba(t)
	duena := solicitudValidaDeRegistro("duena-tope-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro())
	altaDuena, err := pila.registrador.RegistrarPersona(t.Context(), duena)
	if err != nil {
		t.Fatalf("alta de la dueña: %v", err)
	}
	activarCredencialDePrueba(t, pila.pool, altaDuena.CredencialID)

	for i := range maxAvisosDuplicadoPorDueno + 2 {
		registro, err := pila.registrador.RegistrarPersona(t.Context(), altaConDocumento(duena, fmt.Sprintf("intruso-%d-%s@centinela.test", i, uuid.NewString()), celularUnicoDeRegistro()))
		if err != nil || registro.Desafio.ID == uuid.Nil {
			t.Fatalf("intento %d = (%+v, %v), quiero el 201 de relleno también pasado el tope", i, registro, err)
		}
	}

	if total := contarAvisos(t, pila.pool, duena.Correo); total != maxAvisosDuplicadoPorDueno {
		t.Errorf("avisos de la dueña = %d, quiero %d (el tope por dueño)", total, maxAvisosDuplicadoPorDueno)
	}
}
