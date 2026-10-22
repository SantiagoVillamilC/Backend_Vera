package copilot

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegistroLlamada contiene exclusivamente metadatos saneados por el catálogo.
// El identificador de correlación lo genera el servidor, no el proveedor de IA.
type RegistroLlamada struct {
	ID                       uuid.UUID
	Fase, Funcion, Resultado string
	Argumentos               json.RawMessage
	Duracion                 time.Duration
}

// AuditorLlamadas anexa eventos; sus errores impiden continuar con el ciclo.
type AuditorLlamadas interface {
	Registrar(context.Context, RegistroLlamada) error
}

// OpcionCatalogo conecta capacidades del servidor, nunca declaradas por el modelo.
type OpcionCatalogo func(*Catalogo)

// ConAuditoriaLlamadas exige auditoría previa y posterior a cada manejador.
// cmd/api siempre la configura; omitirla sirve para probar el catálogo aislado.
func ConAuditoriaLlamadas(a AuditorLlamadas) OpcionCatalogo {
	return func(c *Catalogo) { c.auditoria = a }
}

// AuditoriaPostgres sólo anexa registros con rol_asistente y verifica su propietario.
type AuditoriaPostgres struct{ pool *pgxpool.Pool }

// NuevaAuditoriaPostgres construye el adaptador sin acceder a la base.
func NuevaAuditoriaPostgres(pool *pgxpool.Pool) *AuditoriaPostgres { return &AuditoriaPostgres{pool} }

type claveMensajeCiclo struct{}

// Registrar rechaza contexto ausente o ajeno, sin confiar en parámetros del modelo.
func (a *AuditoriaPostgres) Registrar(ctx context.Context, r RegistroLlamada) error {
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return ErrNoDisponibleIA
	}
	conv, ok := ConversacionDelContexto(ctx)
	mensaje, _ := ctx.Value(claveMensajeCiclo{}).(uuid.UUID)
	if !ok || mensaje == uuid.Nil || a == nil || a.pool == nil {
		return ErrNoDisponibleIA
	}
	requestID := middleware.GetReqID(ctx)
	if len(requestID) > 128 {
		requestID = ""
	}
	_, err = EnTransaccionAsistente(ctx, a.pool, func(q *repositorio.Queries) (struct{}, error) {
		n, err := q.AnexarLlamadaFuncion(ctx, repositorio.AnexarLlamadaFuncionParams{
			LlamadaID: uuidCiclo(r.ID), Fase: r.Fase, UsuarioID: uuidCiclo(parte), ConversacionID: uuidCiclo(conv), MensajeID: uuidCiclo(mensaje), Funcion: r.Funcion, ArgumentosSaneados: r.Argumentos, Resultado: r.Resultado, DuracionMs: r.Duracion.Milliseconds(), RequestID: requestID, VersionInstrucciones: VersionInstruccionesVera,
		})
		if err != nil || n != 1 {
			return struct{}{}, ErrNoDisponibleIA
		}
		return struct{}{}, nil
	})
	if err != nil {
		return ErrNoDisponibleIA
	}
	return nil
}

func argumentosParaAuditoria(a ArgumentosHerramienta, campos map[string]any) json.RawMessage {
	seguros := make(map[string]any)
	for nombre := range campos {
		switch nombre {
		case "desde":
			seguros[nombre] = a.Desde
		case "hasta":
			seguros[nombre] = a.Hasta
		case "tipo":
			seguros[nombre] = a.Tipo
		case "limite":
			seguros[nombre] = a.Limite
		case "agrupar_por":
			seguros[nombre] = a.AgruparPor
		case "celular":
			seguros[nombre] = celularParaModelo(a.Celular)
		case "nombre":
			seguros[nombre] = nombreParaModelo(a.Nombre)
		case "llave_breb", "destinatario_ref", "transferencia_id", "monto_pesos":
			seguros[nombre] = "[omitido]"
		}
	}
	b, _ := json.Marshal(seguros)
	return b
}
