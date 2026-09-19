package main

import (
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/whatsapp"
)

func TestCanalOTPEligenPorOTPCanal(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		cfg       ConfigAuth
		wantErr   string
		comprobar func(t *testing.T, canal auth.CanalOTP)
	}{
		"simulador": {
			cfg: ConfigAuth{OTPCanal: "simulador", OTPSimuladorCodigoFijo: "123456"},
			comprobar: func(t *testing.T, canal auth.CanalOTP) {
				t.Helper()
				sim, ok := canal.(*otpsim.Simulador)
				if !ok {
					t.Fatalf("canal = %T, quiero *otpsim.Simulador", canal)
				}
				if codigo, hay := sim.CodigoFijo(); !hay || codigo != "123456" {
					t.Errorf("CodigoFijo = (%q, %v), quiero (\"123456\", true)", codigo, hay)
				}
			},
		},
		"cloudapi": {
			cfg: ConfigAuth{OTPCanal: "cloudapi", WhatsAppToken: "tok", WhatsAppPhoneID: "123", WhatsAppTemplate: "otp_login"},
			comprobar: func(t *testing.T, canal auth.CanalOTP) {
				t.Helper()
				if _, ok := canal.(*whatsapp.Cliente); !ok {
					t.Fatalf("canal = %T, quiero *whatsapp.Cliente", canal)
				}
			},
		},
		"demo":        {cfg: ConfigAuth{OTPCanal: "demo"}, wantErr: "demo"},
		"desconocido": {cfg: ConfigAuth{OTPCanal: "otro"}, wantErr: "OTP_CANAL desconocido"},
	}

	for name, tc := range casos {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			canal, err := canalOTP(&Config{Auth: tc.cfg})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("canalOTP error = %v, quiero que contenga %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("canalOTP: %v", err)
			}
			if canal == nil {
				t.Fatal("canalOTP devolvió un canal nil sin error")
			}
			if tc.comprobar != nil {
				tc.comprobar(t, canal)
			}
		})
	}
}

func TestContadoresDeAuthRechazaClienteNil(t *testing.T) {
	t.Parallel()

	if _, err := contadoresDeAuth(nil); err == nil {
		t.Fatal("contadoresDeAuth(nil) devolvió nil, quiero error")
	}
}

func TestCanalOTPRechazaElCodigoFijoInvalido(t *testing.T) {
	t.Parallel()

	_, err := canalOTP(&Config{Auth: ConfigAuth{OTPCanal: "simulador", OTPSimuladorCodigoFijo: "abc"}})
	if err == nil {
		t.Fatal("canalOTP con un código fijo inválido devolvió nil, quiero error")
	}
}
