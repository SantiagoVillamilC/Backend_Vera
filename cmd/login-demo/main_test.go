package main

import "testing"

// TestNormalizarCelular fija el formato E.164 que espera la credencial.
func TestNormalizarCelular(t *testing.T) {
	t.Parallel()
	casos := map[string]string{
		"3001234567":    "+573001234567",
		"+573001234567": "+573001234567",
		"300 123 4567":  "+573001234567",
		" 3001234567 ":  "+573001234567",
		"300123456":     "",
		"30012345678":   "",
		"30012345AB":    "",
		"+57300123456":  "",
		"":              "",
	}
	for entrada, quiero := range casos {
		if obtenido := normalizarCelular(entrada); obtenido != quiero {
			t.Errorf("normalizarCelular(%q) = %q, want %q", entrada, obtenido, quiero)
		}
	}
}

// TestValidarAceptaElUsuarioCompleto comprueba el correo derivado y el E.164.
func TestValidarAceptaElUsuarioCompleto(t *testing.T) {
	t.Parallel()
	pedido, err := validar("123e4567-e89b-12d3-a456-426614174000", "3001234567", "Vera-Demo-2026", "Christian", "")
	if err != nil {
		t.Fatalf("validar: %v", err)
	}
	if pedido.Celular != "+573001234567" {
		t.Fatalf("celular = %q", pedido.Celular)
	}
	if pedido.Correo != "demo-123e4567@centinela.test" {
		t.Fatalf("correo = %q", pedido.Correo)
	}
	if pedido.Nombre != "Christian" || pedido.Contrasena != "Vera-Demo-2026" {
		t.Fatalf("pedido = %+v", pedido)
	}
}

// TestValidarFallaCerrado deja fuera los datos que no sirven para el login.
func TestValidarFallaCerrado(t *testing.T) {
	t.Parallel()
	base := "123e4567-e89b-12d3-a456-426614174000"
	casos := map[string]struct {
		usuario, celular, contrasena, nombre string
	}{
		"usuario no uuid": {usuario: "no-es-uuid", celular: "3001234567", contrasena: "Vera-Demo-2026", nombre: "Christian"},
		"celular corto":   {usuario: base, celular: "300123456", contrasena: "Vera-Demo-2026", nombre: "Christian"},
		"contrasena corta": {
			usuario: base, celular: "3001234567", contrasena: "corta", nombre: "Christian",
		},
		"contrasena larga": {
			usuario: base, celular: "3001234567",
			contrasena: "12345678901234567890123456789012345678901234567890123456789012345",
			nombre:     "Christian",
		},
		"nombre vacio": {usuario: base, celular: "3001234567", contrasena: "Vera-Demo-2026", nombre: "  "},
	}
	for caso, datos := range casos {
		t.Run(caso, func(t *testing.T) {
			t.Parallel()
			if _, err := validar(datos.usuario, datos.celular, datos.contrasena, datos.nombre, ""); err == nil {
				t.Fatalf("validar aceptó %+v", datos)
			}
		})
	}
}
