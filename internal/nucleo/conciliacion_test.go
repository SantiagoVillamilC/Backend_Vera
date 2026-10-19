package nucleo

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// cursorDe arma el timestamptz del cursor como lo devuelve la consulta.
func cursorDe(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// HU-NF-08, criterios 1 y 2.1: la selección es por tiempo de evento y deja fuera lo más reciente que N.
func TestRangoDeConciliacionSeleccionaPorTiempoDeEvento(t *testing.T) {
	t.Parallel()

	ahora := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	cursor := ahora.Add(-10 * time.Minute)

	// N por defecto en 5 minutos: la ventana [cursor, ahora - N) deja fuera los últimos 5 minutos.
	desde, hasta := rangoDeConciliacion(cursorDe(cursor), ahora, 5*time.Minute)
	if !desde.Equal(cursor) {
		t.Errorf("desde = %v, quiero el cursor %v", desde, cursor)
	}
	wantHasta := ahora.Add(-5 * time.Minute)
	if !hasta.Equal(wantHasta) {
		t.Errorf("hasta = %v, quiero %v (ahora menos N)", hasta, wantHasta)
	}

	// N configurable: con 1 minuto solo deja fuera el último minuto.
	_, hastaCorto := rangoDeConciliacion(cursorDe(cursor), ahora, time.Minute)
	if !hastaCorto.Equal(ahora.Add(-time.Minute)) {
		t.Errorf("hasta con N=1m = %v, quiero %v", hastaCorto, ahora.Add(-time.Minute))
	}
	if !hastaCorto.After(hasta) {
		t.Errorf("hasta con N=1m = %v, quiero más reciente que con N=5m (%v)", hastaCorto, hasta)
	}
}

// HU-NF-08, criterio 2.2: cada corrida arranca exactamente en el ventana_hasta de la anterior.
func TestRangoDeConciliacionArrancaEnElCursorAnterior(t *testing.T) {
	t.Parallel()

	primera := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	_, hastaUno := rangoDeConciliacion(pgtype.Timestamptz{}, primera, 5*time.Minute)

	segunda := primera.Add(60 * time.Second)
	desdeDos, hastaDos := rangoDeConciliacion(cursorDe(hastaUno), segunda, 5*time.Minute)
	if !desdeDos.Equal(hastaUno) {
		t.Errorf("desde de la segunda = %v, quiero el ventana_hasta anterior %v", desdeDos, hastaUno)
	}
	if !hastaDos.Equal(segunda.Add(-5 * time.Minute)) {
		t.Errorf("hasta de la segunda = %v, quiero %v", hastaDos, segunda.Add(-5*time.Minute))
	}

	// Aunque una corrida se salte, la siguiente arranca en el mismo cursor: no queda hueco.
	tercera := segunda.Add(120 * time.Second)
	desdeTres, hastaTres := rangoDeConciliacion(cursorDe(hastaDos), tercera, 5*time.Minute)
	if !desdeTres.Equal(hastaDos) || !desdeTres.Equal(desdeDos.Add(60*time.Second)) {
		t.Errorf("desde tras saltar una corrida = %v, quiero %v sin hueco", desdeTres, hastaDos)
	}
	if !hastaTres.After(desdeTres) {
		t.Errorf("hasta = %v, quiero posterior a desde %v", hastaTres, desdeTres)
	}
}

// HU-NF-08, criterio 2.2: sin cursor la primera ventana es vacía y solo siembra el cursor.
func TestRangoDeConciliacionSinCursorEsVacio(t *testing.T) {
	t.Parallel()

	ahora := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	desde, hasta := rangoDeConciliacion(pgtype.Timestamptz{}, ahora, 5*time.Minute)
	if !desde.Equal(hasta) {
		t.Errorf("primera ventana = [%v, %v), quiero vacía", desde, hasta)
	}
	if !hasta.Equal(ahora.Add(-5 * time.Minute)) {
		t.Errorf("hasta = %v, quiero ahora menos N", hasta)
	}
}
