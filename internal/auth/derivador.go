package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/sync/semaphore"
)

// esperaMaximaDerivacion es el tope de una peticion en la cola del semaforo antes de rendirse.
const esperaMaximaDerivacion = 2 * time.Second

// ParametrosArgon2 fija el costo de Argon2id; la memoria se expresa en KiB.
type ParametrosArgon2 struct {
	MemoriaKiB     uint32
	Iteraciones    uint32
	Paralelismo    uint8
	LongitudSal    uint32
	LongitudSalida uint32
}

// ParametrosArgon2Produccion son los 64 MiB, t=3, p=1, salt 16 y salida 32 de la tesis.
func ParametrosArgon2Produccion() ParametrosArgon2 {
	return ParametrosArgon2{MemoriaKiB: 64 * 1024, Iteraciones: 3, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32}
}

// ParametrosArgon2DePrueba bajan el costo para que toda la suite corra rapido.
func ParametrosArgon2DePrueba() ParametrosArgon2 {
	return ParametrosArgon2{MemoriaKiB: 8 * 1024, Iteraciones: 1, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32}
}

// Derivador deriva y verifica contrasenas; A2-07.1 lo reusa para el rehash del login.
type Derivador interface {
	Derivar(ctx context.Context, contrasena string) (string, error)
	Verificar(ctx context.Context, contrasena, phc string) (bool, error)
}

// DerivadorArgon2 es la implementacion pura, sin semaforo, del puerto Derivador.
type DerivadorArgon2 struct{ parametros ParametrosArgon2 }

// NuevoDerivadorArgon2 valida los parametros para no derivar con una sal o salida triviales.
func NuevoDerivadorArgon2(parametros ParametrosArgon2) (*DerivadorArgon2, error) {
	if parametros.MemoriaKiB == 0 || parametros.Iteraciones == 0 || parametros.Paralelismo == 0 {
		return nil, errors.New("auth: Argon2id necesita memoria, iteraciones y paralelismo mayores que cero")
	}
	if parametros.LongitudSal < 8 || parametros.LongitudSalida < 16 {
		return nil, errors.New("auth: Argon2id necesita salt de al menos 8 bytes y salida de al menos 16")
	}
	return &DerivadorArgon2{parametros: parametros}, nil
}

// Derivar genera una sal aleatoria y devuelve el PHC de la contrasena.
func (d *DerivadorArgon2) Derivar(_ context.Context, contrasena string) (string, error) {
	sal := make([]byte, d.parametros.LongitudSal)
	if _, err := rand.Read(sal); err != nil {
		return "", fmt.Errorf("auth: generar la sal de Argon2id: %w", err)
	}
	derivado := argon2.IDKey([]byte(contrasena), sal, d.parametros.Iteraciones, d.parametros.MemoriaKiB, d.parametros.Paralelismo, d.parametros.LongitudSalida)
	return d.parametros.phc(sal, derivado), nil
}

// Verificar lee los parametros del PHC y compara en tiempo constante.
func (d *DerivadorArgon2) Verificar(_ context.Context, contrasena, phc string) (bool, error) {
	parametros, sal, esperado, err := parsearPHC(phc)
	if err != nil {
		return false, err
	}
	derivado := argon2.IDKey([]byte(contrasena), sal, parametros.Iteraciones, parametros.MemoriaKiB, parametros.Paralelismo, uint32(len(esperado))) //nolint:gosec // la longitud del hash Argon2 está acotada por el PHC, cabe en uint32
	return subtle.ConstantTimeCompare(derivado, esperado) == 1, nil
}

// phc serializa el formato $argon2id$v=19$m,t,p$sal$hash en base64 sin relleno.
func (p ParametrosArgon2) phc(sal, derivado []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoriaKiB, p.Iteraciones, p.Paralelismo,
		base64.RawStdEncoding.EncodeToString(sal), base64.RawStdEncoding.EncodeToString(derivado))
}

// parsearPHC desarma el PHC y exige argon2id version 19 con sus tres parametros.
func parsearPHC(phc string) (ParametrosArgon2, []byte, []byte, error) {
	partes := strings.Split(phc, "$")
	if len(partes) != 6 || partes[0] != "" || partes[1] != "argon2id" {
		return ParametrosArgon2{}, nil, nil, errors.New("auth: el valor derivado no es un PHC de argon2id")
	}
	if partes[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return ParametrosArgon2{}, nil, nil, fmt.Errorf("auth: el PHC usa una version de argon2id no soportada %q", partes[2])
	}
	parametros, err := parsearParametrosPHC(partes[3])
	if err != nil {
		return ParametrosArgon2{}, nil, nil, err
	}
	if parametros.MemoriaKiB == 0 || parametros.Iteraciones == 0 || parametros.Paralelismo == 0 {
		return ParametrosArgon2{}, nil, nil, errors.New("auth: el PHC usa memoria, iteraciones o paralelismo en cero")
	}
	sal, err := base64.RawStdEncoding.DecodeString(partes[4])
	if err != nil {
		return ParametrosArgon2{}, nil, nil, fmt.Errorf("auth: la sal del PHC no es base64: %w", err)
	}
	if len(sal) < 8 {
		return ParametrosArgon2{}, nil, nil, errors.New("auth: la sal del PHC tiene menos de 8 bytes")
	}
	esperado, err := base64.RawStdEncoding.DecodeString(partes[5])
	if err != nil {
		return ParametrosArgon2{}, nil, nil, fmt.Errorf("auth: el hash del PHC no es base64: %w", err)
	}
	if len(esperado) < 16 {
		return ParametrosArgon2{}, nil, nil, errors.New("auth: el hash del PHC tiene menos de 16 bytes")
	}
	return parametros, sal, esperado, nil
}

// parsearParametrosPHC lee el segmento m=,t=,p= del PHC.
func parsearParametrosPHC(segmento string) (ParametrosArgon2, error) {
	valores := map[string]string{}
	for _, par := range strings.Split(segmento, ",") {
		clave, valor, ok := strings.Cut(par, "=")
		if !ok {
			return ParametrosArgon2{}, fmt.Errorf("auth: parametro de PHC mal formado %q", par)
		}
		valores[clave] = valor
	}
	memoria, err := strconv.ParseUint(valores["m"], 10, 32)
	if err != nil {
		return ParametrosArgon2{}, errors.New("auth: el PHC no trae la memoria m")
	}
	iteraciones, err := strconv.ParseUint(valores["t"], 10, 32)
	if err != nil {
		return ParametrosArgon2{}, errors.New("auth: el PHC no trae las iteraciones t")
	}
	paralelismo, err := strconv.ParseUint(valores["p"], 10, 8)
	if err != nil {
		return ParametrosArgon2{}, errors.New("auth: el PHC no trae el paralelismo p")
	}
	return ParametrosArgon2{MemoriaKiB: uint32(memoria), Iteraciones: uint32(iteraciones), Paralelismo: uint8(paralelismo)}, nil
}

// DerivadorSemaforizado acota cuantas derivaciones corren a la vez para no reventar la memoria.
type DerivadorSemaforizado struct {
	envuelto Derivador
	semaforo *semaphore.Weighted
}

// NuevoDerivadorSemaforizado exige el derivador y una capacidad positiva.
func NuevoDerivadorSemaforizado(envuelto Derivador, capacidad int) (*DerivadorSemaforizado, error) {
	if envuelto == nil {
		return nil, errors.New("auth: el semaforo de Argon2 necesita el derivador")
	}
	if capacidad < 1 {
		return nil, errors.New("auth: la concurrencia de Argon2 debe ser al menos 1")
	}
	return &DerivadorSemaforizado{envuelto: envuelto, semaforo: semaphore.NewWeighted(int64(capacidad))}, nil
}

// Derivar toma un turno del semaforo y delega; sin turno responde el 503 de auth.
func (d *DerivadorSemaforizado) Derivar(ctx context.Context, contrasena string) (string, error) {
	if err := d.adquirir(ctx); err != nil {
		return "", err
	}
	defer d.semaforo.Release(1)
	return d.envuelto.Derivar(ctx, contrasena)
}

// Verificar toma un turno del semaforo y delega; sin turno responde el 503 de auth.
func (d *DerivadorSemaforizado) Verificar(ctx context.Context, contrasena, phc string) (bool, error) {
	if err := d.adquirir(ctx); err != nil {
		return false, err
	}
	defer d.semaforo.Release(1)
	return d.envuelto.Verificar(ctx, contrasena, phc)
}

// adquirir espera un turno con el tope corto y lo vuelve ErrAuthNoDisponible al vencer.
func (d *DerivadorSemaforizado) adquirir(ctx context.Context) error {
	ctxEspera, cancelar := context.WithTimeout(ctx, esperaMaximaDerivacion)
	defer cancelar()
	if err := d.semaforo.Acquire(ctxEspera, 1); err != nil {
		return ConFailClosed(err)
	}
	return nil
}
