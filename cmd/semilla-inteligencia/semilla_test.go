package main

import (
	"testing"
	"time"
)

// TestVerificarDSNRechazaLoQueNoEsDesechable es la guarda que impide apuntar el sembrador a otra base.
func TestVerificarDSNRechazaLoQueNoEsDesechable(t *testing.T) {
	t.Parallel()
	casos := map[string]struct {
		dsn       string
		permitir  bool
		quieroErr bool
	}{
		"válida": {
			dsn: "postgres://dueno:clave@127.0.0.1:55437/vera_semilla_m8?sslmode=disable",
		},
		"host remoto": {
			dsn:       "postgres://dueno:clave@10.0.0.5:55437/vera_semilla_m8",
			quieroErr: true,
		},
		"host remoto permitido": {
			dsn:      "postgres://dueno:clave@10.0.0.5:55437/vera_semilla_m8",
			permitir: true,
		},
		"puerto de producción": {
			dsn:       "postgres://dueno:clave@127.0.0.1:5432/vera_semilla_m8",
			quieroErr: true,
		},
		"base sin prefijo": {
			dsn:       "postgres://dueno:clave@127.0.0.1:55437/centinela",
			quieroErr: true,
		},
		"vacía": {dsn: "", quieroErr: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			err := VerificarDSN(caso.dsn, Semilla, caso.permitir)
			if (err != nil) != caso.quieroErr {
				t.Fatalf("error = %v, quiero error %v", err, caso.quieroErr)
			}
		})
	}
}

// TestUsuarioDeIndiceEsEstable exige que dos corridas produzcan los mismos usuarios.
func TestUsuarioDeIndiceEsEstable(t *testing.T) {
	t.Parallel()
	primero := usuarioDeIndice(Semilla, 42)
	segundo := usuarioDeIndice(Semilla, 42)
	if primero != segundo {
		t.Fatalf("usuarios distintos: %s y %s", primero, segundo)
	}
	if primero == usuarioDeIndice(Semilla, 43) {
		t.Fatal("dos índices distintos no pueden dar el mismo usuario")
	}
}

// TestPlanDelBancoEsDeterminista compara dos planes con la misma semilla y con otra.
func TestPlanDelBancoEsDeterminista(t *testing.T) {
	t.Parallel()
	opciones := Opciones{Usuarios: 5, Meses: 3, AsientosExtra: 4, Semilla: Semilla}
	asientos, lineas := planDelBanco(opciones)
	otrosAsientos, otrasLineas := planDelBanco(opciones)

	if len(asientos) != len(otrosAsientos) || len(lineas) != len(otrasLineas) {
		t.Fatalf("largos distintos: %d/%d y %d/%d", len(asientos), len(otrosAsientos),
			len(lineas), len(otrasLineas))
	}
	for i := range asientos {
		if asientos[i].indice != otrosAsientos[i].indice || asientos[i].tipo != otrosAsientos[i].tipo ||
			asientos[i].transferencia != otrosAsientos[i].transferencia ||
			asientos[i].descripcion != otrosAsientos[i].descripcion ||
			!asientos[i].ocurrido.Equal(otrosAsientos[i].ocurrido) {
			t.Fatalf("asiento %d distinto: %+v y %+v", i, asientos[i], otrosAsientos[i])
		}
	}
	for i := range lineas {
		if lineas[i] != otrasLineas[i] {
			t.Fatalf("línea %d distinta: %+v y %+v", i, lineas[i], otrasLineas[i])
		}
	}

	conOtraSemilla, _ := planDelBanco(Opciones{Usuarios: 5, Meses: 3, AsientosExtra: 4, Semilla: Semilla + 1})
	if len(conOtraSemilla) > 0 && conOtraSemilla[0].transferencia == asientos[0].transferencia {
		t.Fatal("otra semilla debe producir otro banco")
	}
}

// TestPlanDelBancoCuadra verifica la partida doble y el número de líneas por asiento.
func TestPlanDelBancoCuadra(t *testing.T) {
	t.Parallel()
	asientos, lineas := planDelBanco(Opciones{Usuarios: 3, Meses: 2, AsientosExtra: 2, Semilla: Semilla})

	porAsiento := map[int]int64{}
	for _, linea := range lineas {
		porAsiento[linea.indice] += linea.monto
	}
	for _, asiento := range asientos {
		if suma := porAsiento[asiento.indice]; suma != 0 {
			t.Fatalf("el asiento %d no cuadra: %d", asiento.indice, suma)
		}
	}
	if esperadas := len(asientos) * 2; len(lineas) != esperadas {
		t.Fatalf("líneas = %d, quiero %d", len(lineas), esperadas)
	}
}

// TestPlanDelBancoNoInventaMontosNiFechas fuera del rango declarado.
func TestPlanDelBancoNoInventaMontosNiFechas(t *testing.T) {
	t.Parallel()
	asientos, lineas := planDelBanco(Opciones{Usuarios: 4, Meses: 10, AsientosExtra: 0, Semilla: Semilla})
	for _, linea := range lineas {
		if linea.monto == 0 {
			t.Fatalf("línea sin monto en el asiento %d", linea.indice)
		}
		if linea.monto > 6_000_000_00 || linea.monto < -6_000_000_00 {
			t.Fatalf("monto fuera de rango: %d", linea.monto)
		}
	}
	zona := time.FixedZone("America/Bogota", -5*60*60)
	for _, asiento := range asientos {
		if asiento.ocurrido.Before(time.Date(2026, time.January, 1, 0, 0, 0, 0, zona)) ||
			asiento.ocurrido.After(time.Date(2026, time.October, 31, 23, 59, 59, 0, zona)) {
			t.Fatalf("fecha fuera del rango declarado: %s", asiento.ocurrido)
		}
	}
}
