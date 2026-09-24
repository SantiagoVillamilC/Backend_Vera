package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Servicio resuelve el estado de KYC y ejecuta la verificación sobre el proveedor inyectado.
type Servicio struct {
	pool            *pgxpool.Pool
	proveedor       ProveedorKYC
	proveedorNombre string
	subcuentas      AbridorDeSubcuentas
}

// NuevoServicio arma el servicio con su proveedor y el abridor de subcuentas del núcleo.
func NuevoServicio(pool *pgxpool.Pool, proveedor ProveedorKYC, proveedorNombre string, subcuentas AbridorDeSubcuentas) *Servicio {
	return &Servicio{pool: pool, proveedor: proveedor, proveedorNombre: proveedorNombre, subcuentas: subcuentas}
}

// estadoDeLaParte es el estado derivado o leído junto con el tipo que lo originó.
type estadoDeLaParte struct {
	Estado Estado
	Tipo   string
}

// Estado deriva o lee el producto de la parte; la empresa además materializa su producto y sus subcuentas (P-26).
func (s *Servicio) Estado(ctx context.Context, parteID string) (Estado, error) {
	id, err := uuid.Parse(parteID)
	if err != nil {
		return Estado{}, fmt.Errorf("kyc: la parte %q no es uuid: %w", parteID, err)
	}
	leido, err := s.leerEstado(ctx, id)
	if err != nil {
		return Estado{}, err
	}
	if leido.Tipo == TipoEmpresa {
		if err := s.asegurarSubcuentas(ctx, parteID); err != nil {
			return Estado{}, err
		}
	}
	return leido.Estado, nil
}

// leerEstado lee el tipo y la fila de kyc_estados; sin fila deriva el producto por tipo (criterio 1, H5).
func (s *Servicio) leerEstado(ctx context.Context, id uuid.UUID) (estadoDeLaParte, error) {
	pgID := pgtype.UUID{Bytes: id, Valid: true}
	return auth.EnTransaccion(ctx, s.pool, func(ctx context.Context, q *repositorio.Queries) (estadoDeLaParte, error) {
		tipo, err := q.TipoDeParte(ctx, pgID)
		if err != nil {
			return estadoDeLaParte{}, fmt.Errorf("kyc: leer el tipo de la parte %s: %w", id, err)
		}
		fila, err := q.LeerEstadoKYC(ctx, pgID)
		if err == nil {
			return estadoDeLaParte{Estado: Estado{Producto: fila.Producto, EstadoKYC: fila.EstadoKyc}, Tipo: tipo}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return estadoDeLaParte{}, fmt.Errorf("kyc: leer el estado de la parte %s: %w", id, err)
		}
		derivado, err := estadoPorDefecto(tipo)
		if err != nil {
			return estadoDeLaParte{}, err
		}
		return s.materializar(ctx, q, pgID, tipo, derivado)
	})
}

// materializar asegura la fila de la empresa sin pisar un estado ya verificado.
func (s *Servicio) materializar(ctx context.Context, q *repositorio.Queries, id pgtype.UUID, tipo string, derivado Estado) (estadoDeLaParte, error) {
	if tipo == TipoEmpresa {
		if err := q.AsegurarEstadoKYC(ctx, repositorio.AsegurarEstadoKYCParams{
			ParteID:   id,
			Producto:  derivado.Producto,
			EstadoKyc: derivado.EstadoKYC,
		}); err != nil {
			return estadoDeLaParte{}, fmt.Errorf("kyc: materializar el producto de la empresa: %w", err)
		}
	}
	return estadoDeLaParte{Estado: derivado, Tipo: tipo}, nil
}

// estadoPorDefecto deriva el producto de una parte sin fila: persona bajo monto, empresa depósito ordinario.
func estadoPorDefecto(tipo string) (Estado, error) {
	switch tipo {
	case TipoPersona:
		return Estado{Producto: ProductoBajoMonto, EstadoKYC: EstadoBasico}, nil
	case TipoEmpresa:
		return Estado{Producto: ProductoDepositoOrdinario, EstadoKYC: EstadoPendientePleno}, nil
	default:
		return Estado{}, fmt.Errorf("%w: %q", ErrTipoDeParteDesconocido, tipo)
	}
}

// asegurarSubcuentas llama de forma idempotente al núcleo para las subcuentas de la empresa.
func (s *Servicio) asegurarSubcuentas(ctx context.Context, parteID string) error {
	if s.subcuentas == nil {
		return nil
	}
	if _, err := s.subcuentas.AbrirSubcuentasDeUsuario(ctx, parteID); err != nil {
		return fmt.Errorf("kyc: asegurar las subcuentas de %s: %w", parteID, err)
	}
	return nil
}

// Verificar aplica el nivel pedido: bajo monto sin proveedor o pleno por el proveedor inyectado.
func (s *Servicio) Verificar(ctx context.Context, parteID, nivel, proveedor string) error {
	id, err := uuid.Parse(parteID)
	if err != nil {
		return fmt.Errorf("kyc: la parte %q no es uuid: %w", parteID, err)
	}
	tipo, err := s.tipoDeParte(ctx, id)
	if err != nil {
		return err
	}
	switch nivel {
	case NivelBajoMonto:
		if tipo == TipoEmpresa {
			return ErrNivelNoAplicaEmpresa
		}
		return s.registrarBajoMonto(ctx, id)
	case NivelPleno:
		return s.registrarPleno(ctx, id, tipo, proveedor)
	default:
		return ErrNivelNoSoportado
	}
}

// tipoDeParte lee en transacción corta el tipo de la parte.
func (s *Servicio) tipoDeParte(ctx context.Context, id uuid.UUID) (string, error) {
	return auth.EnTransaccion(ctx, s.pool, func(ctx context.Context, q *repositorio.Queries) (string, error) {
		tipo, err := q.TipoDeParte(ctx, pgtype.UUID{Bytes: id, Valid: true})
		if err != nil {
			return "", fmt.Errorf("kyc: leer el tipo de la parte %s: %w", id, err)
		}
		return tipo, nil
	})
}

// registrarBajoMonto guarda la evidencia sin proveedor y deja el estado básico de la persona.
func (s *Servicio) registrarBajoMonto(ctx context.Context, id uuid.UUID) error {
	_, err := auth.EnTransaccion(ctx, s.pool, func(ctx context.Context, q *repositorio.Queries) (struct{}, error) {
		if err := q.InsertarVerificacionKYC(ctx, repositorio.InsertarVerificacionKYCParams{
			ParteID:         pgtype.UUID{Bytes: id, Valid: true},
			NivelSolicitado: NivelBajoMonto,
			Medio:           MedioRegistro,
			Resultado:       ResultadoVerificado,
			OcurridaEn:      pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}); err != nil {
			return struct{}{}, fmt.Errorf("kyc: registrar la evidencia de bajo monto de %s: %w", id, err)
		}
		return struct{}{}, q.UpsertEstadoKYC(ctx, repositorio.UpsertEstadoKYCParams{
			ParteID:   pgtype.UUID{Bytes: id, Valid: true},
			Producto:  ProductoBajoMonto,
			EstadoKyc: EstadoBasico,
		})
	})
	return err
}

// registrarPleno consulta el proveedor fuera de la transacción y luego guarda evidencia y estado.
func (s *Servicio) registrarPleno(ctx context.Context, id uuid.UUID, tipo, proveedor string) error {
	nombre := s.nombreDeProveedor(proveedor)
	resultado, err := s.proveedor.Verificar(ctx, Solicitud{ParteID: id.String(), Nivel: NivelPleno, Proveedor: nombre})
	if err != nil {
		return fmt.Errorf("kyc: verificar %s con el proveedor: %w", id, err)
	}
	producto, estado := mapearResultadoPleno(tipo, resultado.Estado)
	_, err = auth.EnTransaccion(ctx, s.pool, func(ctx context.Context, q *repositorio.Queries) (struct{}, error) {
		if err := q.InsertarVerificacionKYC(ctx, repositorio.InsertarVerificacionKYCParams{
			ParteID:             pgtype.UUID{Bytes: id, Valid: true},
			NivelSolicitado:     NivelPleno,
			Medio:               resultado.Medio,
			Proveedor:           textoOpcional(nombre),
			Resultado:           resultado.Estado,
			ReferenciaProveedor: textoOpcional(resultado.Referencia),
			OcurridaEn:          pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}); err != nil {
			return struct{}{}, fmt.Errorf("kyc: registrar la evidencia de %s: %w", id, err)
		}
		return struct{}{}, q.UpsertEstadoKYC(ctx, repositorio.UpsertEstadoKYCParams{
			ParteID:   pgtype.UUID{Bytes: id, Valid: true},
			Producto:  producto,
			EstadoKyc: estado,
		})
	})
	return err
}

// mapearResultadoPleno traduce el resultado del proveedor al producto y estado; el producto de la empresa no cambia.
func mapearResultadoPleno(tipo, resultado string) (string, string) {
	if tipo == TipoEmpresa {
		if resultado == ResultadoVerificado {
			return ProductoDepositoOrdinario, EstadoPleno
		}
		if resultado == ResultadoRechazado {
			return ProductoDepositoOrdinario, EstadoRechazado
		}
		return ProductoDepositoOrdinario, EstadoPendientePleno
	}
	switch resultado {
	case ResultadoVerificado:
		return ProductoPleno, EstadoPleno
	case ResultadoRechazado:
		return ProductoBajoMonto, EstadoRechazado
	default:
		return ProductoBajoMonto, EstadoPendientePleno
	}
}

// nombreDeProveedor usa el proveedor pedido por el cliente o el configurado.
func (s *Servicio) nombreDeProveedor(solicitado string) string {
	if solicitado != "" {
		return solicitado
	}
	return s.proveedorNombre
}

// textoOpcional convierte la cadena vacía en un NULL de Postgres.
func textoOpcional(valor string) pgtype.Text {
	return pgtype.Text{String: valor, Valid: valor != ""}
}
