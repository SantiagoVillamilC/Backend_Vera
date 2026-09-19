package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// estadoDesafioPendiente es el único estado que admite verificación.
	estadoDesafioPendiente = "pendiente"
	// estadoDesafioVerificado marca el desafío ya consumido con éxito.
	estadoDesafioVerificado = "verificado"
	// resultadoCambioSolicitado marca la solicitud del cambio con su desafío ya entregado.
	resultadoCambioSolicitado = "solicitado"
	// resultadoCambioContrasenaInvalida marca la contraseña actual que no coincide.
	resultadoCambioContrasenaInvalida = "contrasena_invalida"
	// resultadoCambioConfirmado marca el número nuevo ya aplicado.
	resultadoCambioConfirmado = "confirmado"
	// resultadoCambioDenylistFallida marca los sid que no se pudieron publicar.
	resultadoCambioDenylistFallida = "denylist_fallida"
	// resultadoCambioNotificado marca el aviso que salió por correo y WhatsApp.
	resultadoCambioNotificado = "notificado"
	// resultadoCambioNotificadoSoloCorreo marca el aviso que salió solo por correo, sin opt-in.
	resultadoCambioNotificadoSoloCorreo = "notificado_solo_correo"
	// plantillaCambioNumero es el marcador de plantilla del aviso, pendiente de copy real.
	plantillaCambioNumero = "cambio_numero"
)

// ResultadoCambio es el desenlace del cambio: quién, su contacto y los dos números involucrados.
type ResultadoCambio struct {
	ParteID      uuid.UUID
	CredencialID uuid.UUID
	Correo       string
	CelularViejo string
	CelularNuevo string
	Confirmado   bool
}

// ServicioDeCambioDeNumero exige el factor fuerte y verifica el número nuevo antes de mover el ancla.
type ServicioDeCambioDeNumero struct {
	pool            abridorDeTransaccionesAuth
	desafios        *GestorDesafios
	sesiones        *GestorSesiones
	derivador       Derivador
	contadores      Contadores
	claves          *Claves
	denylist        RevocadorDeSesiones
	eventos         RegistradorEventos
	optIn           LectorOptIn
	racionador      *RacionadorOTP
	pepper          []byte
	contactoSoporte string
	enfriamiento    time.Duration
}

// NuevoServicioDeCambioDeNumero compone el cambio de número, con opt-in y contacto de soporte obligatorios.
func NuevoServicioDeCambioDeNumero(pool *pgxpool.Pool, desafios *GestorDesafios, sesiones *GestorSesiones, derivador Derivador, denylist RevocadorDeSesiones, eventos RegistradorEventos, optIn LectorOptIn, contadores Contadores, claves *Claves, pepper []byte, contactoSoporte string, enfriamiento time.Duration) (*ServicioDeCambioDeNumero, error) {
	if pool == nil || desafios == nil || sesiones == nil || derivador == nil || contadores == nil || claves == nil {
		return nil, errors.New("auth: el cambio de número necesita pool, desafíos, sesiones, derivador, contadores y claves")
	}
	if len(pepper) < 32 {
		return nil, fmt.Errorf("auth: OTP_PEPPER debe tener al menos 32 bytes, tiene %d", len(pepper))
	}
	if enfriamiento <= 0 {
		return nil, errors.New("auth: la ventana de enfriamiento del cambio de número debe ser positiva")
	}
	if contactoSoporte == "" {
		return nil, errors.New("auth: AUTH_CONTACTO_SOPORTE es obligatoria para el aviso del cambio")
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	if optIn == nil {
		optIn = SiempreOptIn{}
	}
	racionador, err := NuevoRacionadorOTP(contadores, claves)
	if err != nil {
		return nil, err
	}
	return &ServicioDeCambioDeNumero{
		pool:            poolDeAuth{pool: pool},
		desafios:        desafios,
		sesiones:        sesiones,
		derivador:       derivador,
		contadores:      contadores,
		claves:          claves,
		denylist:        denylist,
		eventos:         eventos,
		optIn:           optIn,
		racionador:      racionador,
		pepper:          pepper,
		contactoSoporte: contactoSoporte,
		enfriamiento:    enfriamiento,
	}, nil
}

// IniciarCambioDeNumero exige la persona dueña de la sesión, verifica el factor fuerte y entrega el OTP del cambio.
func (s *ServicioDeCambioDeNumero) IniciarCambioDeNumero(ctx context.Context, sesionID uuid.UUID, nuevoCelular, contrasenaActual string, codigoRespaldo *string, origen string) (Desafio, error) {
	credencial, err := s.credencialDeSesion(ctx, sesionID)
	if err != nil {
		return Desafio{}, err
	}
	if err := s.exigirCredencialUsable(credencial); err != nil {
		return Desafio{}, err
	}
	if err := s.exigirSinBloqueo(ctx, PrefijoFallosCuenta+s.claves.ClaveDeCorreo(credencial.Correo)); err != nil {
		return Desafio{}, err
	}
	coincide, err := s.derivador.Verificar(ctx, contrasenaActual, credencial.ValorDerivado.String)
	if err != nil {
		return Desafio{}, fmt.Errorf("auth: verificar la contraseña actual: %w", err)
	}
	if !coincide {
		return Desafio{}, s.contrasenaInvalida(ctx, credencial, origen)
	}
	if err := s.exigirFueraDeEnfriamiento(credencial); err != nil {
		return Desafio{}, err
	}
	nuevo, err := s.numeroNuevoValido(ctx, credencial, nuevoCelular)
	if err != nil {
		return Desafio{}, err
	}
	return s.solicitarDesafioDelCambio(ctx, credencial, nuevo, codigoRespaldo, origen)
}

// ConfirmarCambioDeNumero encadena los dos OTP del camino B y aplica el cambio cuando el número nuevo se verifica.
func (s *ServicioDeCambioDeNumero) ConfirmarCambioDeNumero(ctx context.Context, sesionID, challengeID uuid.UUID, codigo, origen string) (ResultadoCambio, error) {
	conWhatsApp, errOptIn := s.optInDelCambio(ctx, sesionID)
	if errOptIn != nil {
		return ResultadoCambio{}, errOptIn
	}
	var (
		resultado ResultadoCambio
		dominio   error
		hijo      *desafioHijoPendiente
		familias  []uuid.UUID
	)
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		desenlace, err := s.confirmarEn(ctx, q, sesionID, challengeID, codigo, origen, conWhatsApp)
		if err != nil {
			var desafio errorDeDesafio
			if errors.As(err, &desafio) {
				dominio = desafio.error
				return struct{}{}, nil
			}
			return struct{}{}, err
		}
		resultado, hijo, familias = desenlace.resultado, desenlace.hijo, desenlace.familias
		return struct{}{}, nil
	})
	if err != nil {
		return ResultadoCambio{}, err
	}
	if dominio != nil {
		return ResultadoCambio{}, dominio
	}
	if hijo != nil {
		if err := s.desafios.enviarDesafioHijo(ctx, hijo.desafio, hijo.solicitud, hijo.codigo); err != nil {
			return ResultadoCambio{}, err
		}
		return resultado, nil
	}
	if err := s.sesiones.RevocarTodas(ctx, resultado.ParteID); err != nil {
		return ResultadoCambio{}, err
	}
	s.publicarDenylist(ctx, familias)
	return resultado, nil
}

// desenlaceCambio es lo que deja la transacción de confirmación para aplicar después del commit.
type desenlaceCambio struct {
	resultado ResultadoCambio
	hijo      *desafioHijoPendiente
	familias  []uuid.UUID
}

// errorDeDesafio marca el fallo de verificación cuyos intentos y eventos ya escritos deben confirmarse.
type errorDeDesafio struct{ error }

// desafioHijoPendiente es el OTP del número nuevo que se entrega tras confirmar el número viejo.
type desafioHijoPendiente struct {
	desafio   Desafio
	solicitud Solicitud
	codigo    string
}

// confirmarEn resuelve el desafío de la persona y decide entre encadenar el OTP nuevo o aplicar el cambio.
func (s *ServicioDeCambioDeNumero) confirmarEn(ctx context.Context, q *repositorio.Queries, sesionID, challengeID uuid.UUID, codigo, origen string, conWhatsApp bool) (desenlaceCambio, error) {
	credencial, err := q.CredencialDeSesion(ctx, uuidOPgtype(sesionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return desenlaceCambio{}, ErrDesafioInvalido
	}
	if err != nil {
		return desenlaceCambio{}, fmt.Errorf("auth: resolver la credencial del cambio: %w", err)
	}
	fila, err := q.DesafioPorID(ctx, uuidOPgtype(challengeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return desenlaceCambio{}, ErrDesafioInvalido
	}
	if err != nil {
		return desenlaceCambio{}, fmt.Errorf("auth: leer el desafío del cambio: %w", err)
	}
	if uuidDePgtype(fila.CredencialID) != uuidDePgtype(credencial.ID) {
		return desenlaceCambio{}, ErrDesafioInvalido
	}
	if Proposito(fila.Proposito) == PropositoCambioNumeroViejo {
		return s.confirmarNumeroViejo(ctx, q, credencial, fila, challengeID, codigo, origen, conWhatsApp)
	}
	if Proposito(fila.Proposito) == PropositoCambioNumero && fila.Estado == estadoDesafioPendiente {
		return s.aplicarCambio(ctx, q, credencial, uuidDePgtype(fila.ID), codigo, fila.CelularDestino, origen, conWhatsApp)
	}
	return desenlaceCambio{}, ErrDesafioInvalido
}

// confirmarNumeroViejo verifica el OTP del número viejo y, en el segundo confirm, el del número nuevo.
func (s *ServicioDeCambioDeNumero) confirmarNumeroViejo(ctx context.Context, q *repositorio.Queries, credencial repositorio.Credencial, padre repositorio.DesafioPorIDRow, padreID uuid.UUID, codigo, origen string, conWhatsApp bool) (desenlaceCambio, error) {
	switch padre.Estado {
	case estadoDesafioPendiente:
		if _, err := s.desafios.VerificarDesafioEn(ctx, q, padreID, codigo, PropositoCambioNumeroViejo); err != nil {
			return desenlaceCambio{}, errorDeDesafio{err}
		}
		nuevo := padre.CelularNuevo.String
		if !padre.CelularNuevo.Valid || nuevo == "" {
			return desenlaceCambio{}, ErrDesafioInvalido
		}
		solicitud := Solicitud{
			ParteID:        uuidDePgtype(credencial.ParteID),
			CredencialID:   uuidDePgtype(credencial.ID),
			Proposito:      PropositoCambioNumero,
			Celular:        nuevo,
			Origen:         origen,
			CelularNuevo:   nuevo,
			DesafioPadreID: padreID,
		}
		desafio, codigoHijo, err := s.desafios.crearDesafioEn(ctx, q, solicitud, time.Now().UTC())
		if err != nil {
			return desenlaceCambio{}, err
		}
		return desenlaceCambio{
			resultado: ResultadoCambio{
				ParteID:      uuidDePgtype(credencial.ParteID),
				CredencialID: uuidDePgtype(credencial.ID),
				Correo:       credencial.Correo,
				CelularViejo: credencial.Celular,
				CelularNuevo: nuevo,
			},
			hijo: &desafioHijoPendiente{desafio: desafio, solicitud: solicitud, codigo: codigoHijo},
		}, nil
	case estadoDesafioVerificado:
		hijo, err := q.DesafioHijoPendiente(ctx, uuidOPgtype(padreID))
		if errors.Is(err, pgx.ErrNoRows) {
			return desenlaceCambio{}, ErrDesafioInvalido
		}
		if err != nil {
			return desenlaceCambio{}, fmt.Errorf("auth: leer el desafío hijo del cambio: %w", err)
		}
		return s.aplicarCambio(ctx, q, credencial, uuidDePgtype(hijo.ID), codigo, hijo.CelularDestino, origen, conWhatsApp)
	default:
		return desenlaceCambio{}, ErrDesafioInvalido
	}
}

// aplicarCambio verifica el OTP del número nuevo, mueve el ancla y deja las familias para la denylist.
func (s *ServicioDeCambioDeNumero) aplicarCambio(ctx context.Context, q *repositorio.Queries, credencial repositorio.Credencial, desafioID uuid.UUID, codigo, celularNuevo, origen string, conWhatsApp bool) (desenlaceCambio, error) {
	if _, err := s.desafios.VerificarDesafioEn(ctx, q, desafioID, codigo, PropositoCambioNumero); err != nil {
		return desenlaceCambio{}, errorDeDesafio{err}
	}
	if celularNuevo == "" {
		return desenlaceCambio{}, ErrDesafioInvalido
	}
	filas, err := q.ActualizarCelularCredencial(ctx, repositorio.ActualizarCelularCredencialParams{
		Celular: celularNuevo,
		ID:      credencial.ID,
	})
	if err != nil {
		return desenlaceCambio{}, fmt.Errorf("auth: aplicar el cambio de número: %w", err)
	}
	if filas == 0 {
		return desenlaceCambio{}, ErrDesafioInvalido
	}
	familias, err := q.FamiliasVigentesDeParte(ctx, credencial.ParteID)
	if err != nil {
		return desenlaceCambio{}, fmt.Errorf("auth: listar las familias del cambio: %w", err)
	}
	if err := s.encolarAvisosCambio(ctx, q, credencial, celularNuevo, conWhatsApp); err != nil {
		return desenlaceCambio{}, err
	}
	if err := s.registrarConfirmacion(ctx, q, credencial, celularNuevo, origen); err != nil {
		return desenlaceCambio{}, err
	}
	if err := s.registrarNotificacion(ctx, q, credencial, celularNuevo, origen, conWhatsApp); err != nil {
		return desenlaceCambio{}, err
	}
	return desenlaceCambio{
		resultado: ResultadoCambio{
			ParteID:      uuidDePgtype(credencial.ParteID),
			CredencialID: uuidDePgtype(credencial.ID),
			Correo:       credencial.Correo,
			CelularViejo: credencial.Celular,
			CelularNuevo: celularNuevo,
			Confirmado:   true,
		},
		familias: familiasDePgtype(familias),
	}, nil
}

// registrarConfirmacion escribe el evento del cambio aplicado con el celular enmascarado.
func (s *ServicioDeCambioDeNumero) registrarConfirmacion(ctx context.Context, q *repositorio.Queries, credencial repositorio.Credencial, celularNuevo, origen string) error {
	return registrarEvento(ctx, s.eventos, q, Evento{
		Tipo:         TipoEventoCambioNumero,
		Actor:        uuidDePgtype(credencial.ParteID),
		CredencialID: uuidDePgtype(credencial.ID),
		Resultado:    resultadoCambioConfirmado,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
		Detalle:      map[string]string{"celular_enmascarado": enmascararCelular(celularNuevo)},
	})
}

// registrarNotificacion escribe el evento del aviso con los canales por los que salió.
func (s *ServicioDeCambioDeNumero) registrarNotificacion(ctx context.Context, q *repositorio.Queries, credencial repositorio.Credencial, celularNuevo, origen string, conWhatsApp bool) error {
	resultado := resultadoCambioNotificadoSoloCorreo
	if conWhatsApp {
		resultado = resultadoCambioNotificado
	}
	return registrarEvento(ctx, s.eventos, q, Evento{
		Tipo:         TipoEventoCambioNumero,
		Actor:        uuidDePgtype(credencial.ParteID),
		CredencialID: uuidDePgtype(credencial.ID),
		Resultado:    resultado,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
		Detalle:      map[string]string{"celular_enmascarado": enmascararCelular(celularNuevo)},
	})
}

// encolarAvisosCambio encola el aviso al correo y, con opt-in, al número viejo, dentro de la transacción.
func (s *ServicioDeCambioDeNumero) encolarAvisosCambio(ctx context.Context, q *repositorio.Queries, credencial repositorio.Credencial, celularNuevo string, conWhatsApp bool) error {
	base := avisoBaseDeCambio(s.contactoSoporte, celularNuevo, time.Now().UTC().Format(time.RFC3339))
	correo := base
	correo.Destino = DestinoCorreo
	correo.Correo = credencial.Correo
	if err := EncolarAviso(ctx, q, correo); err != nil {
		return err
	}
	if !conWhatsApp || credencial.Celular == "" {
		return nil
	}
	whatsapp := base
	whatsapp.Destino = DestinoWhatsApp
	whatsapp.Celular = credencial.Celular
	return EncolarAviso(ctx, q, whatsapp)
}

// avisoBaseDeCambio arma el aviso del cambio con el número nuevo enmascarado y el contacto de soporte.
func avisoBaseDeCambio(contactoSoporte, celularNuevo, fecha string) Aviso {
	return Aviso{
		Tipo:             TipoAvisoCambioNumero,
		Plantilla:        plantillaCambioNumero,
		NuevoEnmascarado: enmascararCelular(celularNuevo),
		Fecha:            fecha,
		ContactoSoporte:  contactoSoporte,
	}
}

// optInDelCambio resuelve el opt-in de la parte antes de abrir la transacción del cambio.
func (s *ServicioDeCambioDeNumero) optInDelCambio(ctx context.Context, sesionID uuid.UUID) (bool, error) {
	credencial, err := s.credencialDeSesion(ctx, sesionID)
	if errors.Is(err, ErrSesionNoEncontrada) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("auth: resolver la sesión para el opt-in del cambio: %w", err)
	}
	conWhatsApp, err := s.optIn.OptInVigente(ctx, uuidDePgtype(credencial.ParteID))
	if err != nil {
		return false, fmt.Errorf("auth: consultar el opt-in del aviso de cambio: %w", err)
	}
	return conWhatsApp, nil
}

// credencialDeSesion resuelve la credencial de la persona dueña de la familia de sesión, nunca por el contexto.
func (s *ServicioDeCambioDeNumero) credencialDeSesion(ctx context.Context, sesionID uuid.UUID) (repositorio.Credencial, error) {
	credencial, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Credencial, error) {
		return q.CredencialDeSesion(ctx, uuidOPgtype(sesionID))
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return repositorio.Credencial{}, ErrSesionNoEncontrada
	}
	if err != nil {
		return repositorio.Credencial{}, fmt.Errorf("auth: resolver la credencial de la sesión: %w", err)
	}
	return credencial, nil
}

// exigirCredencialUsable rechaza una credencial que no esté activa o no tenga contraseña.
func (s *ServicioDeCambioDeNumero) exigirCredencialUsable(credencial repositorio.Credencial) error {
	if EstadoCredencial(credencial.Estado) != EstadoActivo || !credencial.ValorDerivado.Valid {
		return ErrCredencialesInvalidas
	}
	return nil
}

// exigirSinBloqueo cierra con 429 o 503 antes de tocar Argon2.
func (s *ServicioDeCambioDeNumero) exigirSinBloqueo(ctx context.Context, clave string) error {
	bloqueada, err := s.contadores.Bloqueada(ctx, clave)
	if err != nil {
		return ConFailClosed(err)
	}
	if bloqueada {
		return ErrDemasiadosEnvios
	}
	return nil
}

// contrasenaInvalida acumula el fallo de la cuenta, publica el evento y responde genérico.
func (s *ServicioDeCambioDeNumero) contrasenaInvalida(ctx context.Context, credencial repositorio.Credencial, origen string) error {
	if err := s.racionador.RegistrarFallo(ctx, credencial.Correo); err != nil && !errors.Is(err, ErrDemasiadosEnvios) {
		return err
	}
	if err := registrarEvento(ctx, s.eventos, nil, Evento{
		Tipo:         TipoEventoCambioNumero,
		Actor:        uuidDePgtype(credencial.ParteID),
		CredencialID: uuidDePgtype(credencial.ID),
		Resultado:    resultadoCambioContrasenaInvalida,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
	}); err != nil {
		slog.ErrorContext(ctx, "no se pudo registrar el evento de contraseña inválida del cambio", "err", err)
	}
	return ErrCredencialesInvalidas
}

// exigirFueraDeEnfriamiento rechaza un segundo cambio dentro de la ventana del último confirmado.
func (s *ServicioDeCambioDeNumero) exigirFueraDeEnfriamiento(credencial repositorio.Credencial) error {
	if !credencial.CelularCambiadoEn.Valid {
		return nil
	}
	if time.Since(credencial.CelularCambiadoEn.Time) < s.enfriamiento {
		return ErrCambioNumeroEnEnfriamiento
	}
	return nil
}

// numeroNuevoValido normaliza el número, lo exige distinto del actual y libre en otra credencial no cerrada.
func (s *ServicioDeCambioDeNumero) numeroNuevoValido(ctx context.Context, credencial repositorio.Credencial, nuevoCelular string) (string, error) {
	nuevo, err := NormalizarCelular(nuevoCelular)
	if err != nil {
		return "", err
	}
	if nuevo == credencial.Celular {
		return "", ErrCelularNuevoIgual
	}
	_, err = enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (pgtype.UUID, error) {
		return q.CredencialPorCelularNoCerrada(ctx, repositorio.CredencialPorCelularNoCerradaParams{Celular: nuevo, ID: credencial.ID})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nuevo, nil
	}
	if err != nil {
		return "", fmt.Errorf("auth: verificar el número nuevo libre: %w", err)
	}
	return "", ErrIdentificadorEnUso
}

// solicitarDesafioDelCambio elige el camino del respaldo o el OTP al número viejo y entrega el desafío racionado.
func (s *ServicioDeCambioDeNumero) solicitarDesafioDelCambio(ctx context.Context, credencial repositorio.Credencial, nuevo string, codigoRespaldo *string, origen string) (Desafio, error) {
	solicitud := Solicitud{
		ParteID:           uuidDePgtype(credencial.ParteID),
		CredencialID:      uuidDePgtype(credencial.ID),
		Celular:           nuevo,
		Origen:            origen,
		CorreoNormalizado: credencial.Correo,
	}
	if codigoRespaldo != nil {
		if err := s.consumirRespaldo(ctx, credencial, *codigoRespaldo); err != nil {
			return Desafio{}, err
		}
		solicitud.Proposito = PropositoCambioNumero
	} else {
		solicitud.Proposito = PropositoCambioNumeroViejo
		solicitud.Celular = credencial.Celular
		solicitud.CelularNuevo = nuevo
	}
	desafio, err := s.desafios.CrearDesafioRacionado(ctx, solicitud)
	if err != nil {
		return Desafio{}, err
	}
	s.registrarSolicitud(ctx, credencial, nuevo, solicitud.Proposito, origen)
	return desafio, nil
}

// consumirRespaldo gasta un código de respaldo y cuenta el fallo cuando no coincide.
func (s *ServicioDeCambioDeNumero) consumirRespaldo(ctx context.Context, credencial repositorio.Credencial, codigo string) error {
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		return struct{}{}, ConsumirCodigoRespaldo(ctx, q, s.pepper, uuidDePgtype(credencial.ID), codigo)
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrSegundoFactorRequerido) {
		return fmt.Errorf("auth: consumir el código de respaldo: %w", err)
	}
	if errFallo := s.racionador.RegistrarFallo(ctx, credencial.Correo); errFallo != nil && !errors.Is(errFallo, ErrDemasiadosEnvios) {
		return errFallo
	}
	return ErrSegundoFactorRequerido
}

// registrarSolicitud publica el evento del cambio pedido con el número nuevo enmascarado.
func (s *ServicioDeCambioDeNumero) registrarSolicitud(ctx context.Context, credencial repositorio.Credencial, nuevo string, proposito Proposito, origen string) {
	err := registrarEvento(ctx, s.eventos, nil, Evento{
		Tipo:         TipoEventoCambioNumero,
		Actor:        uuidDePgtype(credencial.ParteID),
		CredencialID: uuidDePgtype(credencial.ID),
		Resultado:    resultadoCambioSolicitado,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
		Detalle:      map[string]string{"celular_enmascarado": enmascararCelular(nuevo), "proposito": string(proposito)},
	})
	if err != nil {
		slog.ErrorContext(ctx, "no se pudo registrar la solicitud del cambio de número", "err", err)
	}
}

// publicarDenylist publica cada familia revocada y deja el evento cuando Redis falla.
func (s *ServicioDeCambioDeNumero) publicarDenylist(ctx context.Context, familias []uuid.UUID) {
	if s.denylist == nil || len(familias) == 0 {
		return
	}
	var fallidas []string
	for _, familia := range familias {
		if err := s.denylist.Revocar(ctx, familia, duracionAccess); err != nil {
			slog.ErrorContext(ctx, "no se pudo publicar el sid del cambio de número en la denylist", "sid", familia.String(), "err", err)
			fallidas = append(fallidas, familia.String())
		}
	}
	if len(fallidas) == 0 {
		return
	}
	if err := registrarEvento(ctx, s.eventos, nil, Evento{
		Tipo:      TipoEventoCambioNumero,
		Resultado: resultadoCambioDenylistFallida,
		Fecha:     time.Now().UTC(),
	}); err != nil {
		slog.ErrorContext(ctx, "no se pudo registrar el evento de denylist fallida del cambio", "err", err)
	}
}

// enmascararCelular deja solo los últimos cuatro dígitos del número.
func enmascararCelular(celular string) string {
	if len(celular) <= 4 {
		return "****"
	}
	return "****" + celular[len(celular)-4:]
}
