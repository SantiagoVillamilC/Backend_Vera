// Package redis construye el cliente de Redis con el presupuesto de fail closed del contrato.
package redis

import (
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// NuevoCliente parsea la URL y aplica los timeouts de 500 ms, sin reintentos y con el timeout por contexto.
func NuevoCliente(url string) (*goredis.Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opts.DialTimeout = 500 * time.Millisecond
	opts.ReadTimeout = 500 * time.Millisecond
	opts.WriteTimeout = 500 * time.Millisecond
	// go-redis trata 0 como el default de 3 reintentos; -1 los desactiva, que es el fail closed.
	opts.MaxRetries = -1
	opts.DialerRetries = 1
	opts.MinRetryBackoff = -1
	opts.MaxRetryBackoff = -1
	opts.ContextTimeoutEnabled = true
	return goredis.NewClient(opts), nil
}
