package appdir

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestMovedRoot(t *testing.T) {
	type c struct{ oldData, newData, root, want string }
	cases := []c{
		// Portable disk got another mount point / drive.
		{"/media/jana/USB1/modelkeeper/data", "/media/jana/USB2/modelkeeper/data", "/media/jana/USB1/STL", "/media/jana/USB2/STL"},
		// Model folder inside the app folder; the app folder moved.
		{"/home/jana/Desktop/modelkeeper/data", "/home/jana/Tools/modelkeeper/data", "/home/jana/Desktop/modelkeeper/Models", "/home/jana/Tools/modelkeeper/Models"},
		// A root elsewhere is not touched.
		{"/media/jana/USB1/modelkeeper/data", "/media/jana/USB2/modelkeeper/data", "/srv/nas/3D", ""},
		// Not moved.
		{"/home/jana/modelkeeper/data", "/home/jana/modelkeeper/data", "/home/jana/STL", ""},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			c{`E:\modelkeeper\data`, `F:\modelkeeper\data`, `E:\STL\Archvillain`, `F:\STL\Archvillain`},
			c{`E:\modelkeeper\data`, `F:\modelkeeper\data`, `\\nas\3D-Modelle`, ``},
			c{`E:\modelkeeper\data`, `f:\MODELKEEPER\data`, `e:\stl`, `f:\stl`})
	}
	for _, x := range cases {
		got, ok := MovedRoot(filepath.FromSlash(x.oldData), filepath.FromSlash(x.newData), filepath.FromSlash(x.root))
		want := filepath.FromSlash(x.want)
		if ok != (x.want != "") || got != want {
			t.Errorf("%s -> %s, root %s: %q %v, want %q", x.oldData, x.newData, x.root, got, ok, want)
		}
	}
}
