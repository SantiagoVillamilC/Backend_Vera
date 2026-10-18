package copilot

import (
	"context"
	"errors"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
)

// CanalAsistente distingue las cuotas de entrada de texto y voz.
type CanalAsistente string

// Canales admitidos por los endpoints del asistente.
const (
	CanalTexto CanalAsistente = "texto"
	CanalVoz   CanalAsistente = "voz"
)

// ErrLimiteUso distingue las cuotas propias de los límites del proveedor externo.
var ErrLimiteUso = errors.New("asistente: límite de uso alcanzado")

// ErrorLimiteUso indica el tiempo restante de la ventana que impide continuar.
type ErrorLimiteUso struct{ ReintentarEn time.Duration }

func (e *ErrorLimiteUso) Error() string { return ErrLimiteUso.Error() }

// Unwrap permite reconocer ErrLimiteUso con errors.Is.
func (e *ErrorLimiteUso) Unwrap() error { return ErrLimiteUso }

// LimitesUso contiene cuotas positivas, ajustables al arrancar el proceso.
type LimitesUso struct {
	TextoHora     int64
	TextoDia      int64
	VozHora       int64
	TokensDiarios int64
	ReservaTokens int64
}

// Validar impide desactivar cuotas con cero y acota los enteros usados por Lua.
func (l LimitesUso) Validar() error {
	for _, n := range []int64{l.TextoHora, l.TextoDia, l.VozHora, l.TokensDiarios, l.ReservaTokens} {
		if n <= 0 || n > 1_000_000_000_000 {
			return errors.New("asistente: las cuotas deben ser positivas y hasta 1000000000000")
		}
	}
	if l.ReservaTokens > l.TokensDiarios {
		return errors.New("asistente: la reserva por llamada supera el presupuesto diario")
	}
	return nil
}

// PresupuestoTokens mantiene consumo y reservas compartidos por todas las réplicas.
// Reservar compara e incrementa atómicamente; Conciliar no revive claves expiradas.
type PresupuestoTokens interface {
	Leer(context.Context, string) (int64, error)
	Reservar(context.Context, string, int64, int64, time.Time) (bool, error)
	Conciliar(context.Context, string, int64) error
}

// ControlUso admite entradas por canal y contabiliza cada vuelta del proveedor.
type ControlUso struct {
	contadores  auth.Contadores
	claves      *auth.Claves
	presupuesto PresupuestoTokens
	limites     LimitesUso
	ahora       func() time.Time
}

// NuevoControlUso exige almacenamiento compartido y configuración válida.
func NuevoControlUso(contadores auth.Contadores, claves *auth.Claves, presupuesto PresupuestoTokens, limites LimitesUso) (*ControlUso, error) {
	if err := limites.Validar(); err != nil {
		return nil, err
	}
	if contadores == nil || claves == nil || presupuesto == nil {
		return nil, errors.New("asistente: dependencias de uso incompletas")
	}
	return &ControlUso{contadores: contadores, claves: claves, presupuesto: presupuesto, limites: limites, ahora: time.Now}, nil
}

var horaColombia = time.FixedZone("America/Bogota", -5*60*60)

func (c *ControlUso) ventana(ctx context.Context) (string, time.Time, time.Time, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, time.Time{}, time.Time{}, err
	}
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return "", time.Time{}, time.Time{}, time.Time{}, err
	}
	if c == nil {
		return "", time.Time{}, time.Time{}, time.Time{}, ErrNoDisponibleIA
	}
	ahora := c.ahora().In(horaColombia)
	finHora := ahora.Truncate(time.Hour).Add(time.Hour)
	finDia := time.Date(ahora.Year(), ahora.Month(), ahora.Day()+1, 0, 0, 0, 0, horaColombia)
	return c.claves.ClaveDeParte(parte.String()), ahora, finHora, finDia, nil
}

func claveTokens(parte string, fin time.Time) string {
	return "vera:tokens:" + parte + ":" + fin.Format("20060102")
}

// Admitir verifica el presupuesto antes de consumir las cuotas de mensajes.
// Las ventanas son horas de reloj y días civiles de Colombia; los intentos cuentan.
func (c *ControlUso) Admitir(ctx context.Context, canal CanalAsistente) error {
	parte, ahora, finHora, finDia, err := c.ventana(ctx)
	if err != nil {
		return err
	}
	if canal != CanalTexto && canal != CanalVoz {
		return ErrSolicitudIA
	}
	consumo, err := c.presupuesto.Leer(ctx, claveTokens(parte, finDia))
	if err != nil {
		return errorUso(ctx)
	}
	if consumo < 0 {
		return ErrNoDisponibleIA
	}
	if consumo > c.limites.TokensDiarios-c.limites.ReservaTokens {
		return &ErrorLimiteUso{ReintentarEn: finDia.Sub(ahora)}
	}
	tipo := string(canal)
	hora := c.limites.TextoHora
	if canal == CanalVoz {
		hora = c.limites.VozHora
	}
	ventanas := []struct {
		clave  string
		fin    time.Time
		limite int64
	}{{"vera:mensajes:" + tipo + ":hora:" + parte + ":" + finHora.Format("2006010215"), finHora, hora}}
	if canal == CanalTexto {
		ventanas = append(ventanas, struct {
			clave  string
			fin    time.Time
			limite int64
		}{"vera:mensajes:texto:dia:" + parte + ":" + finDia.Format("20060102"), finDia, c.limites.TextoDia})
	}
	var espera time.Duration
	for _, v := range ventanas {
		n, err := c.contadores.Incrementar(ctx, v.clave, v.fin.Sub(ahora))
		if err != nil {
			return errorUso(ctx)
		}
		if n <= 0 {
			return ErrNoDisponibleIA
		}
		if n > v.limite && v.fin.Sub(ahora) > espera {
			espera = v.fin.Sub(ahora)
		}
	}
	if espera > 0 {
		return &ErrorLimiteUso{ReintentarEn: espera}
	}
	return nil
}

func errorUso(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrNoDisponibleIA
}

type proveedorConPresupuesto struct {
	control   *ControlUso
	siguiente ProveedorIA
}

// ProtegerProveedor reserva tokens antes de cada llamada, también en function calling.
// Las cuotas de mensajes se consumen sólo en Admitir, una vez por entrada HTTP.
func (c *ControlUso) ProtegerProveedor(p ProveedorIA) ProveedorIA {
	return &proveedorConPresupuesto{control: c, siguiente: p}
}

func (p *proveedorConPresupuesto) Generar(ctx context.Context, s SolicitudIA) (RespuestaIA, error) {
	c := p.control
	parte, ahora, _, fin, err := c.ventana(ctx)
	if err != nil {
		return RespuestaIA{}, err
	}
	if p.siguiente == nil {
		return RespuestaIA{}, ErrNoDisponibleIA
	}
	clave := claveTokens(parte, fin)
	permitido, err := c.presupuesto.Reservar(ctx, clave, c.limites.TokensDiarios, c.limites.ReservaTokens, fin)
	if err != nil {
		return RespuestaIA{}, errorUso(ctx)
	}
	if !permitido {
		return RespuestaIA{}, &ErrorLimiteUso{ReintentarEn: fin.Sub(ahora)}
	}
	respuesta, llamadaErr := p.siguiente.Generar(ctx, s)
	// No se devuelve la reserva si el proveedor omite consumo o si la llamada es incierta.
	if respuesta.TokensEntrada < 0 || respuesta.TokensSalida < 0 {
		return RespuestaIA{}, ErrRespuestaIA
	}
	consumo := int64(respuesta.TokensEntrada) + int64(respuesta.TokensSalida)
	if llamadaErr != nil && consumo < c.limites.ReservaTokens {
		consumo = c.limites.ReservaTokens
	}
	if consumo > 0 {
		cierre, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := c.presupuesto.Conciliar(cierre, clave, consumo-c.limites.ReservaTokens); err != nil {
			return RespuestaIA{}, ErrNoDisponibleIA
		}
	}
	return respuesta, llamadaErr
}
