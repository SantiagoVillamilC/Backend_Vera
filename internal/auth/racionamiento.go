package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	// maxEnviosPorCuenta es el tope de códigos por cuenta en una hora.
	maxEnviosPorCuenta = 5
	// maxEnviosPorOrigen es el tope de códigos por origen en una hora.
	maxEnviosPorOrigen = 20
	// maxFallosPorCuenta es el acumulado de fallos por cuenta en 24 horas.
	maxFallosPorCuenta = 10
	// ventanaEnvios es la ventana de los topes de cuenta y origen.
	ventanaEnvios = time.Hour
	// ventanaFallos es la ventana del acumulado de fallos.
	ventanaFallos = 24 * time.Hour
	// maxAvisosDuplicadoPorDueno es el tope de avisos de registro repetido que recibe un dueño por día.
	maxAvisosDuplicadoPorDueno = 3
	// ventanaAvisosDuplicado es la ventana del tope de avisos por dueño.
	ventanaAvisosDuplicado = 24 * time.Hour
	// esperaBase es el primer bloqueo de la espera creciente.
	esperaBase = time.Minute
	// esperaTope es el tope del bloqueo para no convertir la espera en denegación al titular.
	esperaTope = 15 * time.Minute
)

// RacionadorOTP aplica los topes agregados de envío y de fallos sobre el puerto Contadores.
type RacionadorOTP struct {
	contadores Contadores
	claves     *Claves
}

// NuevoRacionadorOTP exige el puerto y las claves para no degradar el racionamiento a fail open.
func NuevoRacionadorOTP(contadores Contadores, claves *Claves) (*RacionadorOTP, error) {
	if contadores == nil {
		return nil, errors.New("auth: el racionamiento de OTP necesita el puerto Contadores")
	}
	if claves == nil {
		return nil, errors.New("auth: el racionamiento de OTP necesita las claves de contadores")
	}
	return &RacionadorOTP{contadores: contadores, claves: claves}, nil
}

// Exigir comprueba el bloqueo de fallos y cuenta el envío por cuenta y por origen.
func (r *RacionadorOTP) Exigir(ctx context.Context, correoNormalizado, ip string) error {
	if err := r.ExigirCuenta(ctx, correoNormalizado); err != nil {
		return err
	}
	return r.ExigirOrigen(ctx, ip)
}

// ExigirCuenta aplica el bloqueo de fallos y el tope de envíos de la cuenta cuando hay correo.
func (r *RacionadorOTP) ExigirCuenta(ctx context.Context, correoNormalizado string) error {
	if correoNormalizado == "" {
		return nil
	}
	if err := r.exigirSinIncrementar(ctx, PrefijoFallosCuenta+r.claves.ClaveDeCorreo(correoNormalizado)); err != nil {
		return err
	}
	return r.exigirTope(ctx, PrefijoOTPCuenta+r.claves.ClaveDeCorreo(correoNormalizado), maxEnviosPorCuenta, ventanaEnvios)
}

// ExigirOrigen cuenta el envío por IP canonicalizada.
func (r *RacionadorOTP) ExigirOrigen(ctx context.Context, ip string) error {
	return r.exigirTope(ctx, PrefijoOTPOrigen+r.claves.ClaveDeOrigen(ip), maxEnviosPorOrigen, ventanaEnvios)
}

// RegistrarFallo acumula el fallo de la cuenta y bloquea con espera creciente al superar el tope.
func (r *RacionadorOTP) RegistrarFallo(ctx context.Context, correoNormalizado string) error {
	if correoNormalizado == "" {
		return nil
	}
	return r.exigirTope(ctx, PrefijoFallosCuenta+r.claves.ClaveDeCorreo(correoNormalizado), maxFallosPorCuenta, ventanaFallos)
}

// PermitirAvisoDeDuplicado cuenta el aviso del dueño y dice si aún cabe; si Redis falla no avisa y la respuesta no cambia.
func (r *RacionadorOTP) PermitirAvisoDeDuplicado(ctx context.Context, parteID string) bool {
	valor, err := r.contadores.Incrementar(ctx, PrefijoAvisoDuplicado+r.claves.ClaveDeParte(parteID), ventanaAvisosDuplicado)
	if err != nil {
		slog.WarnContext(ctx, "no se pudo contar el aviso de registro repetido, no se envía", "err", err)
		return false
	}
	return valor <= maxAvisosDuplicadoPorDueno
}

// exigirSinIncrementar rechaza la clave ya bloqueada sin consumir un intento nuevo.
func (r *RacionadorOTP) exigirSinIncrementar(ctx context.Context, clave string) error {
	bloqueada, err := r.contadores.Bloqueada(ctx, clave)
	if err != nil {
		return ConFailClosed(err)
	}
	if bloqueada {
		return ErrDemasiadosEnvios
	}
	return nil
}

// exigirTope cuenta un uso y, al superar el tope, bloquea la clave con espera creciente.
func (r *RacionadorOTP) exigirTope(ctx context.Context, clave string, tope int64, ventana time.Duration) error {
	if err := r.exigirSinIncrementar(ctx, clave); err != nil {
		return err
	}
	valor, err := r.contadores.Incrementar(ctx, clave, ventana)
	if err != nil {
		return ConFailClosed(err)
	}
	if valor <= tope {
		return nil
	}
	if err := r.contadores.Bloquear(ctx, clave, esperaCreciente(valor-tope)); err != nil {
		return ConFailClosed(err)
	}
	return ErrDemasiadosEnvios
}

// esperaCreciente duplica la espera desde la base y la topa para que el bloqueo no sea permanente.
func esperaCreciente(incumplimientos int64) time.Duration {
	if incumplimientos < 1 {
		incumplimientos = 1
	}
	espera := esperaBase
	for i := int64(1); i < incumplimientos && espera < esperaTope; i++ {
		espera *= 2
	}
	if espera > esperaTope {
		return esperaTope
	}
	return espera
}
