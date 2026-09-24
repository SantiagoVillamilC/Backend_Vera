package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// resultadoRegistroRelleno marca el alta con un documento ya existente que se respondió sin crear nada.
	resultadoRegistroRelleno = "relleno"
	// plantillaRegistroDuplicado es la plantilla del aviso al dueño real del documento.
	plantillaRegistroDuplicado = "registro_duplicado"
	// restriccionDocumentoUnico es la restricción que impide dos personas con el mismo documento.
	restriccionDocumentoUnico = "personas_documento_uk"
)

// DocumentoDeRegistro identifica al titular que el alta repetida intentó registrar.
type DocumentoDeRegistro struct {
	Tipo   string
	Numero string
}

// RellenadorDeRegistro responde un alta con documento repetido como un alta nueva y avisa al dueño real.
type RellenadorDeRegistro interface {
	RellenarRegistro(ctx context.Context, documento DocumentoDeRegistro, celular, correoNormalizado, origen string) (Desafio, error)
}

// RellenoDeRegistro raciona como el alta real y confirma el relleno con el aviso al dueño en una transacción.
type RellenoDeRegistro struct {
	relleno    InsertadorDeRelleno
	racionador *RacionadorOTP
}

// NuevoRellenoDeRegistro construye el relleno sobre el pool real, los contadores y el OTP_PEPPER.
func NuevoRellenoDeRegistro(pool *pgxpool.Pool, eventos RegistradorEventos, contadores Contadores, claves *Claves, pepper []byte) (*RellenoDeRegistro, error) {
	if pool == nil {
		return nil, errors.New("auth: el relleno del registro necesita el pool")
	}
	if len(pepper) < 32 {
		return nil, fmt.Errorf("auth: OTP_PEPPER debe tener al menos 32 bytes, tiene %d", len(pepper))
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	racionador, err := NuevoRacionadorOTP(contadores, claves)
	if err != nil {
		return nil, err
	}
	return nuevoRellenoDeRegistro(&rellenoPostgres{abridor: poolDeAuth{pool: pool}, eventos: eventos, claves: claves, pepper: pepper}, racionador), nil
}

// nuevoRellenoDeRegistro compone el relleno con sus dos puertos ya validados.
func nuevoRellenoDeRegistro(relleno InsertadorDeRelleno, racionador *RacionadorOTP) *RellenoDeRegistro {
	return &RellenoDeRegistro{relleno: relleno, racionador: racionador}
}

// RellenarRegistro consume la misma ración que el alta real y persiste el relleno con el celular real, sin enviar nada por el canal.
func (r *RellenoDeRegistro) RellenarRegistro(ctx context.Context, documento DocumentoDeRegistro, celular, correoNormalizado, origen string) (Desafio, error) {
	if err := r.racionador.Exigir(ctx, correoNormalizado, origen); err != nil {
		return Desafio{}, err
	}
	return r.relleno.InsertarRelleno(ctx, PropositoRegistro, celular, correoNormalizado, origen, avisarAlDuenoReal(documento, r.racionador))
}

// avisarAlDuenoReal encola el aviso por correo si el documento tiene una credencial viva con el celular verificado y el tope del dueño lo permite.
func avisarAlDuenoReal(documento DocumentoDeRegistro, racionador *RacionadorOTP) EfectoDeRelleno {
	return func(ctx context.Context, q *repositorio.Queries) error {
		dueno, err := q.CredencialVivaPorDocumento(ctx, repositorio.CredencialVivaPorDocumentoParams{
			TipoDocumento: documento.Tipo,
			Documento:     documento.Numero,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("auth: buscar al dueño real del documento repetido: %w", err)
		}
		parteID := uuidDePgtype(dueno.ParteID).String()
		if !dueno.CelularVerificadoEn.Valid || !racionador.PermitirAvisoDeDuplicado(ctx, parteID) {
			return nil
		}
		return EncolarAviso(ctx, q, Aviso{
			Tipo:      TipoAvisoRegistroDuplicado,
			Destino:   DestinoCorreo,
			Correo:    dueno.Correo,
			Plantilla: plantillaRegistroDuplicado,
			ParteID:   parteID,
		})
	}
}

// esDocumentoRepetido reconoce en el error crudo de la transacción la violación de personas_documento_uk.
func esDocumentoRepetido(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == restriccionDocumentoUnico
}

// ConRellenoDeRegistro activa la respuesta de relleno ante un documento repetido; sin ella responde el 409 genérico.
func ConRellenoDeRegistro(relleno RellenadorDeRegistro) OpcionDeRegistrador {
	return func(s *RegistradorDePersonas) { s.relleno = relleno }
}

// rellenaDocumentoRepetido dice si el fallo del alta es el documento repetido y hay relleno configurado.
func (s *RegistradorDePersonas) rellenaDocumentoRepetido(err error) bool {
	return s.relleno != nil && esDocumentoRepetido(err)
}

// registroDeRelleno arma el 201 de una persona con documento repetido: ids aleatorios, sin cuenta y sin OTP.
func (s *RegistradorDePersonas) registroDeRelleno(ctx context.Context, entrada entradaDeRegistro) (Registro, error) {
	desafio, err := s.relleno.RellenarRegistro(ctx, DocumentoDeRegistro{Tipo: entrada.tipoDocumento, Numero: entrada.numeroDocumento}, entrada.celular, entrada.correo, entrada.origen)
	if err != nil {
		return Registro{}, err
	}
	return Registro{
		ParteID:        uuid.New(),
		CredencialID:   uuid.New(),
		Correo:         entrada.correo,
		Celular:        entrada.celular,
		NombreCompleto: entrada.nombreCompleto,
		Estado:         EstadoPendienteVerificacion,
		FechaCreacion:  time.Now().UTC(),
		Desafio:        desafio,
	}, nil
}

// registroDeEmpresaDeRelleno arma el 201 de una empresa cuyo representante repite documento, con empresa_id aleatorio.
func (s *RegistradorDePersonas) registroDeEmpresaDeRelleno(ctx context.Context, entrada entradaDeRegistroEmpresa) (RegistroEmpresa, error) {
	documento := DocumentoDeRegistro{Tipo: entrada.representanteTipoDocumento, Numero: entrada.representanteNumeroDocumento}
	desafio, err := s.relleno.RellenarRegistro(ctx, documento, entrada.celular, entrada.correo, entrada.origen)
	if err != nil {
		return RegistroEmpresa{}, err
	}
	return RegistroEmpresa{
		EmpresaParteID:       uuid.New(),
		RepresentanteParteID: uuid.New(),
		CredencialID:         uuid.New(),
		RazonSocial:          entrada.razonSocial,
		NIT:                  entrada.nit,
		Correo:               entrada.correo,
		Celular:              entrada.celular,
		Estado:               EstadoPendienteVerificacion,
		FechaCreacion:        time.Now().UTC(),
		Desafio:              desafio,
	}, nil
}
