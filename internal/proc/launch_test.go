package proc

import (
	"os"
	"strings"
	"testing"
)

func TestParseJavaClojure(t *testing.T) {
	l := ParseLaunch(strings.Fields("/jdk/bin/java -XX:-OmitStackTraceInFastThrow --enable-native-access=ALL-UNNAMED -Dclojure.basis=.cpcache/1.basis -classpath src:resources:/m2/a.jar:/m2/b.jar clojure.main -m twainmark.cli workspace --port 8901"))
	if l.Runtime != "Java" || l.Main != "clojure.main -m twainmark.cli" {
		t.Fatalf("main = %q (%s)", l.Main, l.Runtime)
	}
	if got := strings.Join(l.AppArgs, " "); got != "workspace --port 8901" {
		t.Fatalf("args = %q", got)
	}
	if len(l.Classpath) != 4 || len(l.Props) != 1 || len(l.Options) != 2 {
		t.Fatalf("cp=%d props=%d opts=%d", len(l.Classpath), len(l.Props), len(l.Options))
	}
	if len(l.Hints) == 0 {
		t.Fatal("expected a Clojure hint")
	}
}

func TestParseJavaJar(t *testing.T) {
	l := ParseLaunch(strings.Fields("java -agentlib:native-image-agent=config-merge-dir=/tmp/x -jar target/chartista.jar gallery --idle-timeout 60s"))
	if l.MainKind != "jar" || l.Main != "target/chartista.jar" {
		t.Fatalf("%+v", l)
	}
	if strings.Join(l.AppArgs, " ") != "gallery --idle-timeout 60s" {
		t.Fatalf("args %v", l.AppArgs)
	}
	if len(l.Options) != 1 {
		t.Fatalf("opts %v", l.Options)
	}
}

func TestParseJavaClassAndModule(t *testing.T) {
	c := ParseLaunch([]string{"java", "-Xmx2g", "-cp", "a.jar", "com.example.Main", "-v"})
	if c.MainKind != "class" || c.Main != "com.example.Main" || len(c.AppArgs) != 1 {
		t.Fatalf("%+v", c)
	}
	m := ParseLaunch([]string{"java", "--add-opens", "java.base/java.lang=ALL-UNNAMED", "-m", "my.mod/my.Main", "x"})
	if m.MainKind != "module" || m.Main != "my.mod/my.Main" || m.Options[0] != "--add-opens java.base/java.lang=ALL-UNNAMED" {
		t.Fatalf("%+v", m)
	}
}

func TestParseScripts(t *testing.T) {
	n := ParseLaunch([]string{"node", "--inspect", "server.js", "--port=3000"})
	if n.Runtime != "Node.js" || n.Main != "server.js" || n.AppArgs[0] != "--port=3000" {
		t.Fatalf("%+v", n)
	}
	p := ParseLaunch([]string{"/usr/bin/python3.12", "-m", "http.server", "8000"})
	if p.Runtime != "Python" || p.MainKind != "module" || p.Main != "http.server" {
		t.Fatalf("%+v", p)
	}
	if o := ParseLaunch([]string{"/bin/ls", "-l"}); o.Main != "" || o.Runtime != "" {
		t.Fatalf("%+v", o)
	}
}

func TestReadArgvForSelf(t *testing.T) {
	argv := readArgv(osGetpid(), "")
	if len(argv) == 0 || !strings.Contains(argv[0], "proc.test") {
		t.Fatalf("argv = %v", argv)
	}
}

func osGetpid() int { return os.Getpid() }
