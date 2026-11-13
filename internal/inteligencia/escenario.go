package inteligencia

import (
	"context"
	"errors"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
)

// ProcesarEscenario enriquece exclusivamente los asientos y el usuario indicados,
// sin avanzar el cursor global ni emitir avisos para otros usuarios.
// Devuelve el número de series detectadas por las reglas de producción.
func (s *Servicio) ProcesarEscenario(ctx context.Context, usuario string, asientos []int64, ahora time.Time) (int, error) {
	if !esUUIDValido(usuario) || len(asientos) == 0 || len(asientos) > 5000 {
		return 0, errors.New("escenario inválido")
	}
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, tx transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
		bloqueado, e := intentarBloqueo(ctx, tx)
		if e != nil {
			return struct{}{}, e
		}
		if !bloqueado {
			return struct{}{}, ErrLoteEnCurso
		}
		lineas, e := q.LineasDeAsientos(ctx, asientos)
		if e != nil {
			return struct{}{}, e
		}
		transferencias, e := q.LineasDeTransferencias(ctx, transferenciasDelLote(lineas))
		if e != nil {
			return struct{}{}, e
		}
		grupos := construirGrupos(asientos, lineas, transferencias)
		nombres, e := resolverNombres(ctx, tx, grupos)
		if e != nil {
			return struct{}{}, e
		}
		memoria, e := memoriaDeUsuario(ctx, q, usuario)
		if e != nil {
			return struct{}{}, e
		}
		resultado := Resultado{PorOrigen: map[string]int{}}
		for _, g := range grupos {
			efectos, e := EfectosPorUsuario(g.apuntes)
			if e != nil {
				return struct{}{}, e
			}
			for _, efecto := range efectos {
				if efecto.UsuarioID != usuario {
					return struct{}{}, errors.New("asiento ajeno al usuario de demo")
				}
				contraparte := ResolverContraparte(g.apuntes, usuario, nombres)
				if e := escribirMovimiento(ctx, q, g, efecto, transaccionDeGrupo(g), contraparte, memoria, s.catalogo(), s.clasificador, s.lexico.ParesDeLexico(), &resultado); e != nil {
					return struct{}{}, e
				}
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return 0, err
	}
	if _, _, err = s.emitirAvisosDeUsuario(ctx, usuario, ahora); err != nil {
		return 0, err
	}
	return enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (int, error) {
		c, e := s.contextoDeAvisos(ctx, q, usuario, ahora)
		if e != nil {
			return 0, e
		}
		maduras := 0
		for _, serie := range c.Series {
			if serie.Estado == avisos.Madura && serie.Indice.EsRecurrente {
				maduras++
			}
		}
		return maduras, nil
	})
}
