package inteligencia

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CursorDeAviso conserva el orden total de la bandeja.
type CursorDeAviso struct {
	Prioridad         int
	SeveridadCentavos int64
	ID                uuid.UUID
	Clave             string
}

// EstadoDeAvisos selecciona una de las dos consultas parciales.
type EstadoDeAvisos string

// Estados admitidos por la bandeja.
const (
	EstadoVigentes    EstadoDeAvisos = "vigentes"
	EstadoDescartados EstadoDeAvisos = "descartados"
)

// PaginaDeAvisos contiene una página y su continuación opaca.
type PaginaDeAvisos struct {
	Avisos    []AvisoEmitido
	Siguiente string
	HayMas    bool
}

// ErrEstadoAvisosInvalido identifica un estado ajeno al contrato.
var ErrEstadoAvisosInvalido = errors.New("estado de avisos inválido")

// CodificarCursorDeAviso codifica sin perder separadores dentro de la clave.
func CodificarCursorDeAviso(c CursorDeAviso) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("v1|%d|%d|%s|%s", c.Prioridad, c.SeveridadCentavos, c.ID, c.Clave)))
}

// DecodificarCursorDeAviso rechaza versiones y valores fuera del dominio SQL.
func DecodificarCursorDeAviso(s string) (CursorDeAviso, error) {
	var c CursorDeAviso
	if len(s) > 16384 {
		return c, ErrCursorInvalido
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || !utf8.Valid(b) {
		return c, ErrCursorInvalido
	}
	p := strings.SplitN(string(b), "|", 5)
	if len(p) != 5 || p[0] != "v1" {
		return c, ErrCursorInvalido
	}
	c.Prioridad, e = strconv.Atoi(p[1])
	if e != nil || c.Prioridad < 1 || c.Prioridad > 32767 {
		return c, ErrCursorInvalido
	}
	c.SeveridadCentavos, e = strconv.ParseInt(p[2], 10, 64)
	if e != nil || c.SeveridadCentavos < 0 {
		return c, ErrCursorInvalido
	}
	c.ID, e = uuid.Parse(p[3])
	if e != nil || c.ID == uuid.Nil || strings.TrimSpace(p[4]) == "" || strings.ContainsRune(p[4], 0) {
		return c, ErrCursorInvalido
	}
	c.Clave = p[4]
	return c, nil
}

type filaDeAviso repositorio.AvisoPorIDRow

// Aviso lee por identificador y propietario en la misma consulta.
func (s *Servicio) Aviso(ctx context.Context, usuario string, id uuid.UUID) (AvisoEmitido, error) {
	f, e := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (repositorio.AvisoPorIDRow, error) {
		return q.AvisoPorID(ctx, repositorio.AvisoPorIDParams{ID: pgtype.UUID{Bytes: id, Valid: true}, UsuarioID: usuario})
	})
	if errors.Is(e, pgx.ErrNoRows) {
		return AvisoEmitido{}, ErrAvisoNoEncontrado
	}
	if e != nil {
		return AvisoEmitido{}, fmt.Errorf("inteligencia: leer aviso: %w", e)
	}
	return avisoDeFila(filaDeAviso(f)), nil
}

// AvisosDelUsuarioPaginados usa una fila extra y un orden estable, sin OFFSET. El periodo es el
// criterio del cupo y del resumen, así que la bandeja y el conteo coinciden para el mismo mes.
func (s *Servicio) AvisosDelUsuarioPaginados(ctx context.Context, usuario string, periodo time.Time, estado EstadoDeAvisos, c *CursorDeAviso, limite int) (PaginaDeAvisos, error) {
	pagina := PaginaDeAvisos{Avisos: make([]AvisoEmitido, 0)}
	if estado != EstadoVigentes && estado != EstadoDescartados {
		return pagina, ErrEstadoAvisosInvalido
	}
	if limite < 1 || limite > 50 {
		return pagina, ErrCursorInvalido
	}
	periodo = inicioDelMesDe(periodo, 0)
	p := repositorio.AvisosVigentesPorPaginaParams{
		UsuarioID: usuario,
		Periodo:   pgtype.Date{Time: periodo, Valid: true},
		Limite:    int32(limite + 1),
	}
	if c != nil {
		if _, e := DecodificarCursorDeAviso(CodificarCursorDeAviso(*c)); e != nil {
			return pagina, e
		}
		p.Prioridad = pgtype.Int2{Int16: int16(c.Prioridad), Valid: true}
		p.Severidad = pgtype.Int8{Int64: c.SeveridadCentavos, Valid: true}
		p.ID = pgtype.UUID{Bytes: c.ID, Valid: true}
		p.Clave = pgtype.Text{String: c.Clave, Valid: true}
	}
	filas, e := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) ([]filaDeAviso, error) {
		resultado := make([]filaDeAviso, 0)
		if estado == EstadoVigentes {
			fs, e := q.AvisosVigentesPorPagina(ctx, p)
			if e != nil {
				return nil, e
			}
			for _, f := range fs {
				resultado = append(resultado, filaDeAviso(f))
			}
		} else {
			fs, e := q.AvisosDescartadosPorPagina(ctx, repositorio.AvisosDescartadosPorPaginaParams(p))
			if e != nil {
				return nil, e
			}
			for _, f := range fs {
				resultado = append(resultado, filaDeAviso(f))
			}
		}
		return resultado, nil
	})
	if e != nil {
		return pagina, e
	}
	pagina.HayMas = len(filas) > limite
	if pagina.HayMas {
		filas = filas[:limite]
	}
	for _, f := range filas {
		pagina.Avisos = append(pagina.Avisos, avisoDeFila(f))
	}
	if pagina.HayMas {
		f := filas[len(filas)-1]
		pagina.Siguiente = CodificarCursorDeAviso(CursorDeAviso{int(f.Prioridad), f.SeveridadCentavos, uuid.UUID(f.ID.Bytes), f.Clave})
	}
	return pagina, nil
}
