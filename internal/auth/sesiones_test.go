package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestNuevoRefreshGenera32BytesYSuSha256(t *testing.T) {
	claro, hash, err := nuevoRefresh()
	if err != nil {
		t.Fatalf("nuevoRefresh: %v", err)
	}

	bruto, err := base64.RawURLEncoding.DecodeString(claro)
	if err != nil {
		t.Fatalf("el refresh en claro no es base64 URL: %v", err)
	}
	if len(bruto) != bytesDeRefresh {
		t.Errorf("refresh en claro = %d bytes, quiero %d", len(bruto), bytesDeRefresh)
	}
	if len(hash) != sha256.Size {
		t.Errorf("hash = %d bytes, quiero %d", len(hash), sha256.Size)
	}
	esperado := sha256.Sum256([]byte(claro))
	if !bytes.Equal(hash, esperado[:]) {
		t.Errorf("el hash no es el SHA-256 del refresh en claro")
	}
	if bytes.Equal(hash, []byte(claro)) {
		t.Errorf("el hash coincide con el refresh en claro")
	}
}

func TestNuevoRefreshNoSeRepite(t *testing.T) {
	primero, _, err := nuevoRefresh()
	if err != nil {
		t.Fatalf("primer refresh: %v", err)
	}
	segundo, _, err := nuevoRefresh()
	if err != nil {
		t.Fatalf("segundo refresh: %v", err)
	}
	if primero == segundo {
		t.Errorf("dos emisiones devolvieron el mismo refresh")
	}
}

func TestVencimientoDeSesionRespetaLosDosTopes(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	casos := map[string]struct {
		ultimoUso     time.Time
		creadaPrimera time.Time
		want          time.Time
	}{
		"familia nueva topa a 30 dias": {
			ultimoUso:     base,
			creadaPrimera: base,
			want:          base.Add(30 * 24 * time.Hour),
		},
		"el tope absoluto de la familia gana cerca de los 45 dias": {
			ultimoUso:     base,
			creadaPrimera: base.Add(-40 * 24 * time.Hour),
			want:          base.Add(5 * 24 * time.Hour),
		},
		"una familia de 46 dias ya vencio por el tope absoluto": {
			ultimoUso:     base,
			creadaPrimera: base.Add(-46 * 24 * time.Hour),
			want:          base.Add(-1 * 24 * time.Hour),
		},
		"la inactividad gana con familia joven": {
			ultimoUso:     base,
			creadaPrimera: base.Add(-10 * 24 * time.Hour),
			want:          base.Add(30 * 24 * time.Hour),
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			got := vencimientoDeSesion(caso.ultimoUso, caso.creadaPrimera)
			if !got.Equal(caso.want) {
				t.Errorf("vencimiento = %s, quiero %s", got, caso.want)
			}
		})
	}
}

func TestEsReusoSoloFueraDeLaVentanaDeGracia(t *testing.T) {
	casos := map[string]struct {
		usado pgtype.Timestamptz
		want  bool
	}{
		"sin usar no es reuso":              {usado: pgtype.Timestamptz{}, want: false},
		"usado hace 5 segundos no es reuso": {usado: pgtype.Timestamptz{Time: time.Now().Add(-5 * time.Second), Valid: true}, want: false},
		"usado hace 20 segundos es reuso":   {usado: pgtype.Timestamptz{Time: time.Now().Add(-20 * time.Second), Valid: true}, want: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			if got := esReuso(caso.usado); got != caso.want {
				t.Errorf("esReuso = %v, quiero %v", got, caso.want)
			}
		})
	}
}

func TestHashDeRefreshEsEstable(t *testing.T) {
	if !bytes.Equal(hashDeRefresh("abc"), hashDeRefresh("abc")) {
		t.Errorf("el hash del mismo refresh cambió")
	}
	if bytes.Equal(hashDeRefresh("abc"), hashDeRefresh("abd")) {
		t.Errorf("dos refresh distintos dieron el mismo hash")
	}
}
