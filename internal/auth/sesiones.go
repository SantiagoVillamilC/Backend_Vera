package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// bytesDeRefresh es el tamaño del refresh opaco antes de codificarlo.
	bytesDeRefresh = 32
	// ventanaDeGracia admite un segundo uso del mismo refresh sin tratarlo como reuso.
	ventanaDeGracia = 15 * time.Second
	// vigenciaInactividad es el tope de vida de un refresh desde su último uso.
	vigenciaInactividad = 30 * 24 * time.Hour
	// vigenciaAbsolutaFamilia es el tope de vida de la familia desde su primera sesión.
	vigenciaAbsolutaFamilia = 45 * 24 * time.Hour
	// resultadoSesion es el resultado de los eventos de sesión que no traen un valor propio.
	resultadoSesion = "ok"
)

// Sesion es el estado de una sesión de auth, sin el refresh ni su hash.
type Sesion struct {
	ID             uuid.UUID
	FamiliaID      uuid.UUID
	ParteID        uuid.UUID
	ContextoActivo string
	Dispositivo    string
	CreadaEn       time.Time
	UltimoUsoEn    time.Time
	VenceEn        time.Time
}

// RolVigente es un rol no revocado de una parte sobre la parte representada.
type RolVigente struct {
	Rol            string
	RepresentadaID uuid.UUID
}

// sesionConRefresh es el par que devuelven la emisión y la rotación, con la familia reusada si la hubo.
type sesionConRefresh struct {
	refresh        string
	sesion         Sesion
	familiaReusada uuid.UUID
	reusado        bool
}

// GestorSesiones emite, rota y revoca sesiones, y publica cada cambio en el trail.
type GestorSesiones struct {
	pool    abridorDeTransaccionesAuth
	eventos RegistradorEventos
}

// NuevoGestorSesiones construye el gestor; sin registrador usa el nulo de la fase 0.
func NuevoGestorSesiones(pool *pgxpool.Pool, eventos RegistradorEventos) *GestorSesiones {
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	return &GestorSesiones{pool: poolDeAuth{pool: pool}, eventos: eventos}
}

// EmitirSesion abre una familia nueva y devuelve el refresh en claro con su sesión.
func (g *GestorSesiones) EmitirSesion(ctx context.Context, parteID uuid.UUID, contexto, dispositivo string) (string, Sesion, error) {
	out, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (sesionConRefresh, error) {
		refresh, sesion, err := g.emitirSesionEn(ctx, q, parteID, contexto, dispositivo)
		return sesionConRefresh{refresh: refresh, sesion: sesion}, err
	})
	if err != nil {
		return "", Sesion{}, err
	}
	return out.refresh, out.sesion, nil
}

// emitirSesionEn inserta la familia nueva y su evento sobre la transacción abierta del llamador.
func (g *GestorSesiones) emitirSesionEn(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, contexto, dispositivo string) (string, Sesion, error) {
	ahora := time.Now().UTC()
	refresh, fila, err := g.crearSesion(ctx, q, parteID, uuid.New(), contexto, dispositivo, ahora, ahora)
	if err != nil {
		return "", Sesion{}, err
	}
	sesion := sesionDeFila(fila)
	err = registrarEvento(ctx, g.eventos, q, Evento{
		Tipo:      TipoEventoEmisionSesion,
		Actor:     sesion.ParteID,
		SesionID:  sesion.ID,
		Resultado: resultadoSesion,
		Fecha:     ahora,
		Contexto:  sesion.ContextoActivo,
	})
	if err != nil {
		return "", Sesion{}, err
	}
	return refresh, sesion, nil
}

// RotarRefresh marca el refresh como usado e inserta su sucesor en la misma familia.
func (g *GestorSesiones) RotarRefresh(ctx context.Context, refreshEnClaro string) (string, Sesion, error) {
	refresh, sesion, _, err := g.RotarRefreshConSid(ctx, refreshEnClaro)
	return refresh, sesion, err
}

// RotarRefreshConSid rota y devuelve la familia reusada para publicarla en la denylist.
func (g *GestorSesiones) RotarRefreshConSid(ctx context.Context, refreshEnClaro string) (string, Sesion, uuid.UUID, error) {
	out, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (sesionConRefresh, error) {
		return g.rotarRefreshEn(ctx, q, refreshEnClaro)
	})
	if err != nil {
		return "", Sesion{}, uuid.Nil, err
	}
	if out.reusado {
		return "", Sesion{}, out.familiaReusada, ErrRefreshReusado
	}
	return out.refresh, out.sesion, uuid.Nil, nil
}

// rotarRefreshEn ejecuta la rotación y la detección de reuso sobre la transacción abierta.
func (g *GestorSesiones) rotarRefreshEn(ctx context.Context, q *repositorio.Queries, refreshEnClaro string) (sesionConRefresh, error) {
	hash := hashDeRefresh(refreshEnClaro)
	usada, err := q.RotarSesion(ctx, hash)
	if err == nil {
		return g.sucesoraDe(ctx, q, usada)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sesionConRefresh{}, fmt.Errorf("auth: rotar el refresh: %w", err)
	}
	return g.refreshSinFilas(ctx, q, hash)
}

// RevocarSesion revoca una sesión de la parte; si ya estaba revocada o no es suya, no falla.
func (g *GestorSesiones) RevocarSesion(ctx context.Context, parteID, sesionID uuid.UUID) error {
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		filas, err := q.RevocarSesionDeParte(ctx, repositorio.RevocarSesionDeParteParams{
			ID:      pgtype.UUID{Bytes: sesionID, Valid: true},
			ParteID: pgtype.UUID{Bytes: parteID, Valid: true},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: revocar la sesión: %w", err)
		}
		if filas == 0 {
			return struct{}{}, nil
		}
		return struct{}{}, g.publicarRevocacion(ctx, q, parteID, sesionID, "")
	})
	return err
}

// RevocarFamilia revoca todas las sesiones de la familia.
func (g *GestorSesiones) RevocarFamilia(ctx context.Context, familiaID uuid.UUID) error {
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		if _, err := q.RevocarFamilia(ctx, pgtype.UUID{Bytes: familiaID, Valid: true}); err != nil {
			return struct{}{}, fmt.Errorf("auth: revocar la familia: %w", err)
		}
		return struct{}{}, g.publicarRevocacion(ctx, q, uuid.Nil, uuid.Nil, familiaID.String())
	})
	return err
}

// RevocarTodas revoca todas las sesiones vigentes de la parte.
func (g *GestorSesiones) RevocarTodas(ctx context.Context, parteID uuid.UUID) error {
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		if _, err := q.RevocarSesionesDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true}); err != nil {
			return struct{}{}, fmt.Errorf("auth: revocar las sesiones de la parte: %w", err)
		}
		return struct{}{}, g.publicarRevocacion(ctx, q, parteID, uuid.Nil, "")
	})
	return err
}

// ListarActivas devuelve las sesiones no revocadas, no usadas y vigentes de la parte.
func (g *GestorSesiones) ListarActivas(ctx context.Context, parteID uuid.UUID) ([]Sesion, error) {
	filas, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) ([]repositorio.Sesion, error) {
		return q.ListarSesionesActivas(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
	})
	if err != nil {
		return nil, err
	}
	sesiones := make([]Sesion, 0, len(filas))
	for _, fila := range filas {
		sesiones = append(sesiones, sesionDeFila(fila))
	}
	return sesiones, nil
}

// RolesVigentesDeParte resuelve los roles de la parte en cada emisión; la consulta la reusan A2-07, A2-08 y A1-06.
func (g *GestorSesiones) RolesVigentesDeParte(ctx context.Context, parteID uuid.UUID) ([]RolVigente, error) {
	return enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) ([]RolVigente, error) {
		return g.rolesVigentesEn(ctx, q, parteID)
	})
}

// rolesVigentesEn resuelve los roles vigentes dentro de la transacción abierta del llamador.
func (g *GestorSesiones) rolesVigentesEn(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID) ([]RolVigente, error) {
	filas, err := q.RolesVigentesDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("auth: resolver los roles de la parte: %w", err)
	}
	roles := make([]RolVigente, 0, len(filas))
	for _, fila := range filas {
		roles = append(roles, RolVigente{Rol: fila.Rol, RepresentadaID: uuidDePgtype(fila.RepresentadaID)})
	}
	return roles, nil
}

// resolverContextoEn convierte el contexto pedido en el contexto_id concreto y su rol.
func (g *GestorSesiones) resolverContextoEn(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, solicitado string) (string, string, error) {
	if solicitado == "" || solicitado == ContextoUsuario {
		return ContextoUsuario, "", nil
	}
	if solicitado != ContextoEmpresa {
		return "", "", fmt.Errorf("auth: contexto de sesión desconocido %q", solicitado)
	}
	roles, err := g.rolesVigentesEn(ctx, q, parteID)
	if err != nil {
		return "", "", err
	}
	if len(roles) == 0 {
		return "", "", ErrSinRolDeEmpresa
	}
	return ContextoDeEmpresa(roles[0].RepresentadaID), roles[0].Rol, nil
}

// usuarioEn arma el usuario de la sesión con el tipo de cuenta y el nombre del contexto activo.
func (g *GestorSesiones) usuarioEn(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, contexto, rol string) (DatosUsuario, error) {
	fila, err := q.UsuarioDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
	if err != nil {
		return DatosUsuario{}, fmt.Errorf("auth: leer el usuario de la parte: %w", err)
	}
	usuario := DatosUsuario{
		ID:             parteID,
		TipoCuenta:     fila.Tipo,
		Correo:         fila.Correo.String,
		NombreCompleto: fila.NombreCompleto.String,
		ContextoActivo: contexto,
		RolActivo:      rol,
	}
	if empresaID, esEmpresa := EmpresaDeContexto(contexto); esEmpresa {
		filaEmpresa, err := q.UsuarioDeParte(ctx, pgtype.UUID{Bytes: empresaID, Valid: true})
		if err != nil {
			return DatosUsuario{}, fmt.Errorf("auth: leer el usuario de la empresa: %w", err)
		}
		if !filaEmpresa.RazonSocial.Valid {
			return DatosUsuario{}, fmt.Errorf("auth: la empresa %s no tiene razón social", empresaID)
		}
		usuario.ID = empresaID
		usuario.TipoCuenta = filaEmpresa.Tipo
		usuario.RazonSocial = filaEmpresa.RazonSocial.String
		usuario.NombreCompleto = ""
	}
	return usuario, nil
}

// crearSesion inserta una sesión de la familia y devuelve su refresh en claro con la fila persistida.
func (g *GestorSesiones) crearSesion(ctx context.Context, q *repositorio.Queries, parteID, familiaID uuid.UUID, contexto, dispositivo string, ahora, creadaPrimera time.Time) (string, repositorio.Sesion, error) {
	refresh, hash, err := nuevoRefresh()
	if err != nil {
		return "", repositorio.Sesion{}, err
	}
	fila, err := q.InsertarSesion(ctx, repositorio.InsertarSesionParams{
		ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
		FamiliaID:      pgtype.UUID{Bytes: familiaID, Valid: true},
		ParteID:        pgtype.UUID{Bytes: parteID, Valid: true},
		ContextoActivo: contexto,
		Dispositivo:    dispositivo,
		RefreshHash:    hash,
		UltimoUsoEn:    pgtype.Timestamptz{Time: ahora, Valid: true},
		VenceEn:        pgtype.Timestamptz{Time: vencimientoDeSesion(ahora, creadaPrimera), Valid: true},
	})
	if err != nil {
		return "", repositorio.Sesion{}, fmt.Errorf("auth: insertar la sesión: %w", err)
	}
	return refresh, fila, nil
}

// sucesoraDe inserta el sucesor de la sesión rotada y publica el evento de rotación.
func (g *GestorSesiones) sucesoraDe(ctx context.Context, q *repositorio.Queries, usada repositorio.Sesion) (sesionConRefresh, error) {
	primera, err := q.PrimeraSesionDeFamilia(ctx, usada.FamiliaID)
	if err != nil {
		return sesionConRefresh{}, fmt.Errorf("auth: primera sesión de la familia: %w", err)
	}
	ahora := time.Now().UTC()
	refresh, fila, err := g.crearSesion(ctx, q, uuidDePgtype(usada.ParteID), uuidDePgtype(usada.FamiliaID), usada.ContextoActivo, usada.Dispositivo, ahora, primera.Time)
	if err != nil {
		return sesionConRefresh{}, err
	}
	sesion := sesionDeFila(fila)
	err = registrarEvento(ctx, g.eventos, q, Evento{
		Tipo:      TipoEventoRotacion,
		Actor:     sesion.ParteID,
		SesionID:  sesion.ID,
		Resultado: resultadoSesion,
		Fecha:     ahora,
		Contexto:  sesion.ContextoActivo,
	})
	if err != nil {
		return sesionConRefresh{}, err
	}
	return sesionConRefresh{refresh: refresh, sesion: sesion}, nil
}

// refreshSinFilas decide entre reuso y ventana de gracia cuando la rotación no encontró fila.
func (g *GestorSesiones) refreshSinFilas(ctx context.Context, q *repositorio.Queries, hash []byte) (sesionConRefresh, error) {
	fila, err := q.SesionPorRefreshHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return sesionConRefresh{}, ErrRefreshInvalido
	}
	if err != nil {
		return sesionConRefresh{}, fmt.Errorf("auth: buscar la sesión del refresh: %w", err)
	}
	if !esReuso(fila.UsadoEn) {
		return sesionConRefresh{}, ErrRefreshInvalido
	}
	if _, err := q.RevocarFamilia(ctx, fila.FamiliaID); err != nil {
		return sesionConRefresh{}, fmt.Errorf("auth: revocar la familia reusada: %w", err)
	}
	err = registrarEvento(ctx, g.eventos, q, Evento{
		Tipo:      TipoEventoReusoDetectado,
		Actor:     uuidDePgtype(fila.ParteID),
		SesionID:  uuidDePgtype(fila.ID),
		Resultado: resultadoSesion,
		Fecha:     time.Now().UTC(),
		Contexto:  fila.ContextoActivo,
		Detalle:   map[string]string{"dispositivo": fila.Dispositivo},
	})
	if err != nil {
		return sesionConRefresh{}, err
	}
	return sesionConRefresh{reusado: true, familiaReusada: uuidDePgtype(fila.FamiliaID)}, nil
}

// publicarRevocacion escribe el evento de revocación con el actor y la sesión disponibles.
func (g *GestorSesiones) publicarRevocacion(ctx context.Context, q *repositorio.Queries, actor, sesionID uuid.UUID, contexto string) error {
	return registrarEvento(ctx, g.eventos, q, Evento{
		Tipo:      TipoEventoRevocacion,
		Actor:     actor,
		SesionID:  sesionID,
		Resultado: resultadoSesion,
		Fecha:     time.Now().UTC(),
		Contexto:  contexto,
	})
}

// nuevoRefresh genera 32 bytes aleatorios y devuelve el refresh en claro y su SHA-256.
func nuevoRefresh() (string, []byte, error) {
	bruto := make([]byte, bytesDeRefresh)
	if _, err := rand.Read(bruto); err != nil {
		return "", nil, fmt.Errorf("auth: generar el refresh: %w", err)
	}
	claro := base64.RawURLEncoding.EncodeToString(bruto)
	suma := sha256.Sum256([]byte(claro))
	return claro, suma[:], nil
}

// hashDeRefresh calcula el SHA-256 del refresh en claro que presenta el cliente.
func hashDeRefresh(claro string) []byte {
	suma := sha256.Sum256([]byte(claro))
	return suma[:]
}

// vencimientoDeSesion devuelve el menor entre el tope de inactividad y el tope absoluto de la familia.
func vencimientoDeSesion(ultimoUso, creadaPrimera time.Time) time.Time {
	topeInactividad := ultimoUso.Add(vigenciaInactividad)
	topeFamilia := creadaPrimera.Add(vigenciaAbsolutaFamilia)
	if topeInactividad.Before(topeFamilia) {
		return topeInactividad
	}
	return topeFamilia
}

// esReuso reporta si el refresh ya usado quedó fuera de la ventana de gracia.
func esReuso(usado pgtype.Timestamptz) bool {
	return usado.Valid && time.Since(usado.Time) > ventanaDeGracia
}

// sesionDeFila mapea la fila del repositorio al tipo de dominio.
func sesionDeFila(fila repositorio.Sesion) Sesion {
	return Sesion{
		ID:             uuidDePgtype(fila.ID),
		FamiliaID:      uuidDePgtype(fila.FamiliaID),
		ParteID:        uuidDePgtype(fila.ParteID),
		ContextoActivo: fila.ContextoActivo,
		Dispositivo:    fila.Dispositivo,
		CreadaEn:       fila.CreadaEn.Time,
		UltimoUsoEn:    fila.UltimoUsoEn.Time,
		VenceEn:        fila.VenceEn.Time,
	}
}

// uuidDePgtype convierte el uuid de pgx al uuid de Go.
func uuidDePgtype(id pgtype.UUID) uuid.UUID { return uuid.UUID(id.Bytes) }
