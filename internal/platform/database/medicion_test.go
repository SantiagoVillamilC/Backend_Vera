package database

import (
	"errors"
	"testing"
)

// TestDSNLocalDeMedicion impide que un comando de medición escriba en una base real.
func TestDSNLocalDeMedicion(t *testing.T) {
	t.Parallel()
	casos := map[string]struct {
		dsn       string
		permitir  bool
		quieroErr bool
	}{
		"válido": {
			dsn: "postgres://dueno:clave@127.0.0.1:55437/vera_semilla_m8?sslmode=disable",
		},
		"localhost": {
			dsn: "postgres://dueno:clave@localhost:55437/vera_carga_m8",
		},
		"host remoto": {
			dsn:       "postgres://dueno:clave@10.1.2.3:55437/vera_semilla_m8",
			quieroErr: true,
		},
		"host remoto permitido": {
			dsn:      "postgres://dueno:clave@10.1.2.3:55437/vera_semilla_m8",
			permitir: true,
		},
		"puerto real": {
			dsn:       "postgres://dueno:clave@127.0.0.1:5432/vera_semilla_m8",
			quieroErr: true,
		},
		"base sin prefijo": {
			dsn:       "postgres://dueno:clave@127.0.0.1:55437/centinela",
			quieroErr: true,
		},
		"vacío": {dsn: "", quieroErr: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			err := DSNLocalDeMedicion(caso.dsn, "55437", "vera_", caso.permitir)
			if (err != nil) != caso.quieroErr {
				t.Fatalf("error = %v, quiero error %v", err, caso.quieroErr)
			}
			if err != nil && !errors.Is(err, ErrDSNNoDesechable) {
				t.Fatalf("error = %v, quiero el centinela ErrDSNNoDesechable", err)
			}
		})
	}
}
