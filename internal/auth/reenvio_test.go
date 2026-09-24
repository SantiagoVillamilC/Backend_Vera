package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestEsperaCrecienteDuplicaYTopa(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		incumplimientos int64
		want            time.Duration
	}{
		"cero parte de la base":        {incumplimientos: 0, want: time.Minute},
		"el primero es un minuto":      {incumplimientos: 1, want: time.Minute},
		"el segundo duplica":           {incumplimientos: 2, want: 2 * time.Minute},
		"el tercero vuelve a duplicar": {incumplimientos: 3, want: 4 * time.Minute},
		"el cuarto llega a ocho":       {incumplimientos: 4, want: 8 * time.Minute},
		"el quinto topa en quince":     {incumplimientos: 5, want: 15 * time.Minute},
		"el tope no crece mas":         {incumplimientos: 50, want: 15 * time.Minute},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if got := esperaCreciente(caso.incumplimientos); got != caso.want {
				t.Errorf("esperaCreciente(%d) = %s, quiero %s", caso.incumplimientos, got, caso.want)
			}
		})
	}
}

func TestHayEnfriamiento(t *testing.T) {
	t.Parallel()

	ahora := time.Unix(1_700_000_000, 0).UTC()
	casos := map[string]struct {
		ultimo time.Time
		want   bool
	}{
		"recien enviado esta en enfriamiento": {ultimo: ahora.Add(-1 * time.Second), want: true},
		"a los cincuenta y nueve segundos":    {ultimo: ahora.Add(-59 * time.Second), want: true},
		"a los sesenta segundos ya no":        {ultimo: ahora.Add(-60 * time.Second), want: false},
		"muy viejo ya no":                     {ultimo: ahora.Add(-2 * time.Hour), want: false},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if got := hayEnfriamiento(caso.ultimo, ahora); got != caso.want {
				t.Errorf("hayEnfriamiento(%s) = %v, quiero %v", ahora.Sub(caso.ultimo), got, caso.want)
			}
		})
	}
}

func TestEsRellenoLeeLaMarcaExplicitaOElCentinela(t *testing.T) {
	t.Parallel()

	parte := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	credencial := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}
	casos := map[string]struct {
		fila repositorio.AuthDesafio
		want bool
	}{
		"relleno de recuperacion con el centinela": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoRecuperacion), CelularDestino: celularDeRelleno},
			want: true,
		},
		"relleno de registro con el centinela no exige propósito recuperacion": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoRegistro), CelularDestino: celularDeRelleno},
			want: true,
		},
		"relleno de registro con celular real y sin parte ni credencial": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoRegistro), CelularDestino: "+573001234567"},
			want: true,
		},
		"desafio real con parte y celular raro no es relleno": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoLogin), ParteID: parte, CelularDestino: "raro"},
			want: false,
		},
		"el centinela con parte presente sigue siendo relleno por el lado seguro": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoRecuperacion), ParteID: parte, CelularDestino: celularDeRelleno},
			want: true,
		},
		"el centinela con credencial presente sigue siendo relleno por el lado seguro": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoRecuperacion), CredencialID: credencial, CelularDestino: celularDeRelleno},
			want: true,
		},
		"recuperacion real con parte y credencial no es relleno": {
			fila: repositorio.AuthDesafio{Proposito: string(PropositoRecuperacion), ParteID: parte, CredencialID: credencial, CelularDestino: "+573001234567"},
			want: false,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if got := esRelleno(caso.fila); got != caso.want {
				t.Errorf("esRelleno(%+v) = %v, quiero %v", caso.fila, got, caso.want)
			}
		})
	}
}

func TestDecidirReenvio(t *testing.T) {
	t.Parallel()

	ahora := time.Unix(1_700_000_000, 0).UTC()
	filaBase := repositorio.DesafioPendientePorIDRow{
		UltimoEnvioEn: pgtype.Timestamptz{Time: ahora.Add(-2 * time.Minute), Valid: true},
	}
	casos := map[string]struct {
		fila repositorio.DesafioPendientePorIDRow
		want error
	}{
		"pendiente y en regla reenvia": {
			fila: filaBase,
			want: nil,
		},
		"expirado no reenvia": {
			fila: func() repositorio.DesafioPendientePorIDRow {
				f := filaBase
				f.Expirado = true
				return f
			}(),
			want: ErrDesafioInvalido,
		},
		"en enfriamiento no reenvia": {
			fila: func() repositorio.DesafioPendientePorIDRow {
				f := filaBase
				f.UltimoEnvioEn = pgtype.Timestamptz{Time: ahora.Add(-30 * time.Second), Valid: true}
				return f
			}(),
			want: ErrEnfriamientoReenvio,
		},
		"con tres reenvios agotados": {
			fila: func() repositorio.DesafioPendientePorIDRow {
				f := filaBase
				f.Reenvios = 3
				return f
			}(),
			want: ErrDemasiadosReenvios,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if got := decidirReenvio(caso.fila, ahora); !errors.Is(got, caso.want) {
				t.Errorf("decidirReenvio = %v, quiero %v", got, caso.want)
			}
		})
	}
}
