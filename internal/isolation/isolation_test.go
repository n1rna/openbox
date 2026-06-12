package isolation

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		spec string
		want Isolation
		err  bool
	}{
		{"", Isolation{Kind: Native}, false},
		{"native", Isolation{Kind: Native}, false},
		{"docker:ubuntu:22.04", Isolation{Kind: Docker, Image: "ubuntu:22.04"}, false},
		{"nspawn:/var/lib/machines/x", Isolation{Kind: Nspawn, Image: "/var/lib/machines/x"}, false},
		{"docker:", Isolation{}, true},
		{"nspawn:", Isolation{}, true},
		{"firecracker:x", Isolation{}, true},
	}
	for _, c := range cases {
		got, err := Parse(c.spec)
		if c.err {
			if err == nil {
				t.Errorf("Parse(%q): want error", c.spec)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): %v", c.spec, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.spec, got, c.want)
		}
	}
}

func TestArgv(t *testing.T) {
	d := Isolation{Kind: Docker, Image: "alpine"}
	if got := d.OneShotArgv("echo hi"); !reflect.DeepEqual(got,
		[]string{"docker", "run", "--rm", "alpine", "/bin/sh", "-c", "echo hi"}) {
		t.Errorf("docker one-shot argv = %v", got)
	}
	if got := d.ShellArgv(); !reflect.DeepEqual(got,
		[]string{"docker", "run", "-i", "--rm", "alpine", "/bin/sh"}) {
		t.Errorf("docker shell argv = %v", got)
	}
	n := Default()
	if got := n.OneShotArgv("ls"); !reflect.DeepEqual(got, []string{"/bin/sh", "-c", "ls"}) {
		t.Errorf("native one-shot argv = %v", got)
	}
}
