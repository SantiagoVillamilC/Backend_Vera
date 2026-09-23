package auth

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/unicode/norm"
)

const (
	// motivoRegistroEmpresa es el motivo corto de la transicion inicial del representante.
	motivoRegistroEmpresa = "registro_empresa"
	// contextoRegistroEmpresa marca el evento de alta de la parte juridica.
	contextoRegistroEmpresa = "registro_empresa"
	// rolRepresentanteLegal ata al representante con la empresa representada.
	rolRepresentanteLegal = "representante_legal"
	// tipoPartePersona y tipoParteEmpresa son los dos tipos de parte del alta.
	tipoPartePersona = "persona"
	tipoParteEmpresa = "empresa"
	// maxRunasRazonSocial acota la razon social.
	maxRunasRazonSocial = 200
)

// SolicitudDeRegistroEmpresa es el alta de empresa que el handler entrega al servicio.
type SolicitudDeRegistroEmpresa struct {
	RazonSocial                  string
	NIT                          string
	Correo                       string
	Contrasena                   string
	Celular                      string
	RepresentanteNombre          string
	RepresentanteTipoDocumento   string
	RepresentanteNumeroDocumento string
	Consentimientos              []SolicitudDeConsentimiento
	Origen                       string
	UserAgent                    string
}

// RegistroEmpresa es la empresa creada con su representante, sin ningun secreto.
type RegistroEmpresa struct {
	EmpresaParteID       uuid.UUID
	RepresentanteParteID uuid.UUID
	CredencialID         uuid.UUID
	RazonSocial          string
	NIT                  string
	Correo               string
	Celular              string
	Estado               EstadoCredencial
	FechaCreacion        time.Time
	Desafio              Desafio
}

// NormalizarNIT quita puntos, guiones y espacios, y exige el digito de verificacion DIAN.
func NormalizarNIT(nit string) (string, error) {
	limpio := strings.NewReplacer(".", "", "-", "", " ", "").Replace(strings.TrimSpace(nit))
	if !nitValido(limpio) {
		return "", ErrNitInvalido
	}
	return limpio, nil
}

// nitValido aplica el modulo 11 DIAN sobre el cuerpo y compara con el digito de verificacion.
func nitValido(nit string) bool {
	if len(nit) < 2 || len(nit) > 15 {
		return false
	}
	cuerpo, verif := nit[:len(nit)-1], nit[len(nit)-1]
	if verif < '0' || verif > '9' {
		return false
	}
	pesos := []int{3, 7, 13, 17, 19, 23, 29, 37, 41, 43, 47, 53, 59, 67, 71}
	suma := 0
	for i := 0; i < len(cuerpo); i++ {
		d := cuerpo[len(cuerpo)-1-i]
		if d < '0' || d > '9' || i >= len(pesos) {
			return false
		}
		suma += int(d-'0') * pesos[i]
	}
	digito := suma % 11
	if digito > 1 {
		digito = 11 - digito
	}
	return digito == int(verif-'0')
}

// RegistrarEmpresa valida, deriva y persiste la parte juridica con su representante y su rol.
func (s *RegistradorDePersonas) RegistrarEmpresa(ctx context.Context, solicitud SolicitudDeRegistroEmpresa) (RegistroEmpresa, error) {
	entrada, err := normalizarSolicitudDeRegistroEmpresa(solicitud)
	if err != nil {
		return RegistroEmpresa{}, err
	}
	phc, err := s.derivador.Derivar(ctx, solicitud.Contrasena)
	if err != nil {
		return RegistroEmpresa{}, err
	}
	datos, err := s.persistirEmpresa(ctx, entrada, phc)
	if s.rellenaDocumentoRepetido(err) {
		return s.registroDeEmpresaDeRelleno(ctx, entrada)
	}
	if err != nil {
		return RegistroEmpresa{}, MapearErrorDeConsulta(err)
	}
	s.abrirSubcuentasEnSegundoPlano(ctx, datos.representanteParteID)
	s.abrirSubcuentasEnSegundoPlano(ctx, datos.empresaParteID)
	desafio, err := s.crearDesafioDeRegistroEmpresa(ctx, datos, entrada)
	if err != nil {
		s.compensarAlta(ctx, datos.credencialID)
		return RegistroEmpresa{}, err
	}
	return datos.resultado(desafio), nil
}

// CrearEmpresaParaParte crea la parte juridica de una persona ya existente y le cuelga el rol.
func (s *RegistradorDePersonas) CrearEmpresaParaParte(ctx context.Context, parteID uuid.UUID, razonSocial, nit string) (uuid.UUID, error) {
	razon, err := normalizarRazonSocial(razonSocial)
	if err != nil {
		return uuid.Nil, err
	}
	nitCanonico, err := NormalizarNIT(nit)
	if err != nil {
		return uuid.Nil, err
	}
	empresaID, err := enTransaccion(ctx, poolDeAuth{pool: s.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (uuid.UUID, error) {
			return s.escribirEmpresaParaParte(ctx, q, parteID, razon, nitCanonico)
		})
	if err != nil {
		return uuid.Nil, MapearErrorDeConsulta(err)
	}
	s.abrirSubcuentasEnSegundoPlano(ctx, empresaID)
	return empresaID, nil
}

// persistirEmpresa escribe representante, empresa y rol en una sola transaccion.
func (s *RegistradorDePersonas) persistirEmpresa(ctx context.Context, entrada entradaDeRegistroEmpresa, phc string) (datosEmpresaPersistidos, error) {
	return enTransaccion(ctx, poolDeAuth{pool: s.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (datosEmpresaPersistidos, error) {
			return s.escribirCuentaDeEmpresa(ctx, q, entrada, phc)
		})
}

// verificarNitLibre falla con ErrIdentificadorEnUso si el NIT ya existe, sin mirar estado porque empresas_nit_uk es total.
func verificarNitLibre(ctx context.Context, q *repositorio.Queries, nit string) error {
	existe, err := q.EmpresaExistePorNit(ctx, nit)
	if err != nil {
		return fmt.Errorf("auth: comprobar el NIT del alta: %w", err)
	}
	if existe {
		return ErrIdentificadorEnUso
	}
	return nil
}

// escribirCuentaDeEmpresa inserta representante, credencial, empresa y rol, y devuelve sus ids.
func (s *RegistradorDePersonas) escribirCuentaDeEmpresa(ctx context.Context, q *repositorio.Queries, entrada entradaDeRegistroEmpresa, phc string) (datosEmpresaPersistidos, error) {
	if err := verificarCatalogoDeConsentimientos(ctx, q, entrada.consentimientos); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	if err := verificarIdentificadoresLibres(ctx, q, entrada.correo, entrada.celular); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	if err := verificarNitLibre(ctx, q, entrada.nit); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	ahora := time.Now().UTC()
	representanteParteID, credencialID, empresaParteID := uuid.New(), uuid.New(), uuid.New()
	if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: uuidOPgtype(representanteParteID), Tipo: tipoPartePersona}); err != nil {
		return datosEmpresaPersistidos{}, fmt.Errorf("auth: insertar la parte del representante: %w", err)
	}
	if _, err := q.CrearPersona(ctx, repositorio.CrearPersonaParams{
		ParteID:       uuidOPgtype(representanteParteID),
		TipoDocumento: entrada.representanteTipoDocumento,
		Documento:     entrada.representanteNumeroDocumento,
		Nombre:        entrada.representanteNombre,
	}); err != nil {
		return datosEmpresaPersistidos{}, fmt.Errorf("auth: insertar la persona del representante: %w", err)
	}
	if _, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            uuidOPgtype(credencialID),
		ParteID:       uuidOPgtype(representanteParteID),
		Correo:        entrada.correo,
		Celular:       entrada.celular,
		ValorDerivado: pgtype.Text{String: phc, Valid: true},
		Estado:        string(EstadoPendienteVerificacion),
	}); err != nil {
		return datosEmpresaPersistidos{}, fmt.Errorf("auth: insertar la credencial del representante: %w", err)
	}
	if err := RegistrarTransicion(ctx, q, credencialID, "", EstadoPendienteVerificacion, motivoRegistroEmpresa); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	if err := s.registrarConsentimientos(ctx, q, representanteParteID, entrada.consentimientos, entrada.origen, entrada.userAgent, ahora); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	if err := s.escribirEmpresaParaParteEn(ctx, q, representanteParteID, empresaParteID, entrada.razonSocial, entrada.nit); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	if err := registrarEvento(ctx, s.eventos, q, Evento{
		Tipo:         TipoEventoRegistro,
		Actor:        representanteParteID,
		CredencialID: credencialID,
		Resultado:    resultadoRegistroCreado,
		Fecha:        ahora,
		Origen:       entrada.origen,
		Contexto:     contextoRegistroEmpresa,
	}); err != nil {
		return datosEmpresaPersistidos{}, err
	}
	return datosEmpresaPersistidos{
		empresaParteID:       empresaParteID,
		representanteParteID: representanteParteID,
		credencialID:         credencialID,
		razonSocial:          entrada.razonSocial,
		nit:                  entrada.nit,
		correo:               entrada.correo,
		celular:              entrada.celular,
		creadaEn:             ahora,
	}, nil
}

// escribirEmpresaParaParte inserta la parte juridica, su empresa y el rol del representante.
func (s *RegistradorDePersonas) escribirEmpresaParaParte(ctx context.Context, q *repositorio.Queries, representanteParteID uuid.UUID, razonSocial, nit string) (uuid.UUID, error) {
	empresaParteID := uuid.New()
	if err := s.escribirEmpresaParaParteEn(ctx, q, representanteParteID, empresaParteID, razonSocial, nit); err != nil {
		return uuid.Nil, err
	}
	return empresaParteID, nil
}

// escribirEmpresaParaParteEn escribe la parte empresa, la fila de empresas y el rol representante_legal.
func (s *RegistradorDePersonas) escribirEmpresaParaParteEn(ctx context.Context, q *repositorio.Queries, representanteParteID, empresaParteID uuid.UUID, razonSocial, nit string) error {
	if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: uuidOPgtype(empresaParteID), Tipo: tipoParteEmpresa}); err != nil {
		return fmt.Errorf("auth: insertar la parte de la empresa: %w", err)
	}
	if _, err := q.CrearEmpresa(ctx, repositorio.CrearEmpresaParams{
		ParteID:     uuidOPgtype(empresaParteID),
		Nit:         nit,
		RazonSocial: razonSocial,
	}); err != nil {
		return fmt.Errorf("auth: insertar la empresa: %w", err)
	}
	if _, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
		ParteID:        uuidOPgtype(representanteParteID),
		Rol:            rolRepresentanteLegal,
		RepresentadaID: uuidOPgtype(empresaParteID),
	}); err != nil {
		return fmt.Errorf("auth: insertar el rol del representante: %w", err)
	}
	return nil
}

// crearDesafioDeRegistroEmpresa crea el desafio racionado al celular del representante.
func (s *RegistradorDePersonas) crearDesafioDeRegistroEmpresa(ctx context.Context, datos datosEmpresaPersistidos, entrada entradaDeRegistroEmpresa) (Desafio, error) {
	return s.desafios.CrearDesafioRacionado(ctx, Solicitud{
		ParteID:           datos.representanteParteID,
		CredencialID:      datos.credencialID,
		Proposito:         PropositoRegistro,
		Celular:           datos.celular,
		Origen:            entrada.origen,
		CorreoNormalizado: datos.correo,
	})
}

// entradaDeRegistroEmpresa reúne los datos ya normalizados del alta de empresa.
type entradaDeRegistroEmpresa struct {
	razonSocial                  string
	nit                          string
	correo                       string
	celular                      string
	representanteNombre          string
	representanteTipoDocumento   string
	representanteNumeroDocumento string
	origen                       string
	userAgent                    string
	consentimientos              []consentimientoResuelto
}

// datosEmpresaPersistidos es lo que la transaccion deja listo para el desafio y la respuesta.
type datosEmpresaPersistidos struct {
	empresaParteID       uuid.UUID
	representanteParteID uuid.UUID
	credencialID         uuid.UUID
	razonSocial          string
	nit                  string
	correo               string
	celular              string
	creadaEn             time.Time
}

// resultado arma el RegistroEmpresa de respuesta con el desafio ya emitido.
func (d datosEmpresaPersistidos) resultado(desafio Desafio) RegistroEmpresa {
	return RegistroEmpresa{
		EmpresaParteID:       d.empresaParteID,
		RepresentanteParteID: d.representanteParteID,
		CredencialID:         d.credencialID,
		RazonSocial:          d.razonSocial,
		NIT:                  d.nit,
		Correo:               d.correo,
		Celular:              d.celular,
		Estado:               EstadoPendienteVerificacion,
		FechaCreacion:        d.creadaEn,
		Desafio:              desafio,
	}
}

// normalizarSolicitudDeRegistroEmpresa normaliza razon social, NIT, correo, celular y representante.
func normalizarSolicitudDeRegistroEmpresa(solicitud SolicitudDeRegistroEmpresa) (entradaDeRegistroEmpresa, error) {
	razonSocial, err := normalizarRazonSocial(solicitud.RazonSocial)
	if err != nil {
		return entradaDeRegistroEmpresa{}, err
	}
	nit, err := NormalizarNIT(solicitud.NIT)
	if err != nil {
		return entradaDeRegistroEmpresa{}, err
	}
	celular, err := NormalizarCelular(solicitud.Celular)
	if err != nil {
		return entradaDeRegistroEmpresa{}, err
	}
	nombre, err := NormalizarNombreCompleto(solicitud.RepresentanteNombre)
	if err != nil {
		return entradaDeRegistroEmpresa{}, err
	}
	consentimientos, err := normalizarConsentimientos(solicitud.Consentimientos)
	if err != nil {
		return entradaDeRegistroEmpresa{}, err
	}
	return entradaDeRegistroEmpresa{
		razonSocial:                  razonSocial,
		nit:                          nit,
		correo:                       NormalizarCorreo(solicitud.Correo),
		celular:                      celular,
		representanteNombre:          nombre,
		representanteTipoDocumento:   solicitud.RepresentanteTipoDocumento,
		representanteNumeroDocumento: solicitud.RepresentanteNumeroDocumento,
		origen:                       solicitud.Origen,
		userAgent:                    solicitud.UserAgent,
		consentimientos:              consentimientos,
	}, nil
}

// normalizarRazonSocial valida que la razon social no este vacia ni pase el tope de runas.
func normalizarRazonSocial(razon string) (string, error) {
	normalizada := norm.NFC.String(strings.TrimSpace(razon))
	if normalizada == "" || utf8.RuneCountInString(normalizada) > maxRunasRazonSocial || strings.ContainsRune(normalizada, 0) {
		return "", ErrRazonSocialInvalida
	}
	return normalizada, nil
}
