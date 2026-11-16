package estado

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCargarSinArchivoDevuelveApagado(t *testing.T) {
	a, err := NuevoAlmacen(filepath.Join(t.TempDir(), "estado"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := a.Cargar()
	if err != nil || e.Fase != Apagado || e.PodID != "" {
		t.Fatalf("e=%+v err=%v", e, err)
	}
}

func TestGuardarYCargarConPermisos0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "estado")
	a, err := NuevoAlmacen(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := Estado{Fase: Listo, PodID: "abc123_x-Y", GPU: "NVIDIA RTX A5000", TarifaUSDH: 0.27,
		SesionInicio: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC), UltimaActividad: time.Date(2026, 11, 15, 10, 5, 0, 0, time.UTC)}
	if err := a.Guardar(original); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "estado.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permisos %v, esperado 0600", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(dir)
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("permisos del directorio %v, esperado 0700", dirInfo.Mode().Perm())
	}
	leido, err := a.Cargar()
	if err != nil {
		t.Fatal(err)
	}
	if !leido.SesionInicio.Equal(original.SesionInicio) || leido.PodID != original.PodID || leido.Fase != Listo || leido.TarifaUSDH != 0.27 {
		t.Fatalf("leido %+v", leido)
	}
}

func TestGuardarRechazaIdDePodPeligroso(t *testing.T) {
	a, _ := NuevoAlmacen(t.TempDir())
	for _, id := range []string{"../../etc", "a b", "-rf", "x;y"} {
		if err := a.Guardar(Estado{Fase: Listo, PodID: id}); !errors.Is(err, ErrEstadoInvalido) {
			t.Fatalf("id %q: err=%v", id, err)
		}
	}
}

func TestCargarRechazaFaseDesconocidaYJsonRoto(t *testing.T) {
	dir := t.TempDir()
	a, _ := NuevoAlmacen(dir)
	for _, contenido := range []string{`{"fase":"volando"}`, `{no es json`, `{"fase":"listo","pod_id":"../x"}`} {
		if err := os.WriteFile(filepath.Join(dir, "estado.json"), []byte(contenido), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Cargar(); !errors.Is(err, ErrEstadoInvalido) {
			t.Fatalf("contenido %q: err=%v", contenido, err)
		}
	}
}

func TestRutaKnownHostsPorPod(t *testing.T) {
	a, _ := NuevoAlmacen(t.TempDir())
	r1, err := a.RutaKnownHosts("pod1")
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := a.RutaKnownHosts("pod2")
	if r1 == r2 || filepath.Base(r1) != "pod1" {
		t.Fatalf("%s %s", r1, r2)
	}
	if _, err := a.RutaKnownHosts("../x"); !errors.Is(err, ErrEstadoInvalido) {
		t.Fatalf("err=%v", err)
	}
}
