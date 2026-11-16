//go:build unix

package estado

import "testing"

func TestBloqueoDelServicioEsExclusivo(t *testing.T) {
	a, _ := NuevoAlmacen(t.TempDir())
	liberar, ok, err := a.BloquearServicio()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, otro, err := a.BloquearServicio(); err != nil || otro {
		t.Fatalf("un segundo bloqueo debe fallar sin error: ok=%v err=%v", otro, err)
	}
	liberar()
	if l2, ok, err := a.BloquearServicio(); err != nil || !ok {
		t.Fatalf("tras liberar debe poder bloquearse: ok=%v err=%v", ok, err)
	} else {
		l2()
	}
}
