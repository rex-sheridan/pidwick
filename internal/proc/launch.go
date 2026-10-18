package proc

import (
	"archive/zip"
	"bufio"
	"encoding/binary"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Launch is a structured reading of a process's command line. Raw command
// lines (especially Java's) are dominated by classpaths and flags, so this
// pulls out what the program actually is: its main class/jar/script, its own
// arguments, and the runtime options.
type Launch struct {
	Argv      []string
	Runtime   string // "Java", "Node.js", "Python", ...
	MainKind  string // class, jar, module, script
	Main      string
	AppArgs   []string
	Options   []string    // runtime flags (-X..., --enable-..., agents)
	Props     [][2]string // -Dkey=value
	Classpath []string
	Hints     []string
	JarInfo   map[string]string // from the jar manifest
}

// readArgv returns the exact argv of a process via the kernel, falling back
// to splitting the ps command line (ambiguous when arguments contain spaces).
func readArgv(pid int, fallback string) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err == nil && len(raw) > 4 {
		argc := int(binary.LittleEndian.Uint32(raw[:4]))
		rest := raw[4:]
		// skip exec_path and the NUL padding after it
		i := 0
		for i < len(rest) && rest[i] != 0 {
			i++
		}
		for i < len(rest) && rest[i] == 0 {
			i++
		}
		var argv []string
		for len(argv) < argc && i < len(rest) {
			j := i
			for j < len(rest) && rest[j] != 0 {
				j++
			}
			argv = append(argv, string(rest[i:j]))
			i = j + 1
		}
		if len(argv) == argc && argc > 0 {
			return argv
		}
	}
	return strings.Fields(fallback)
}

// ParseLaunch interprets argv.
func ParseLaunch(argv []string) Launch {
	l := Launch{Argv: argv}
	if len(argv) == 0 {
		return l
	}
	exe := strings.ToLower(filepath.Base(argv[0]))
	switch {
	case exe == "java" || exe == "javaw":
		l.parseJava(argv[1:])
	default:
		l.parseScript(exe, argv[1:])
	}
	return l
}

var javaTakesValue = map[string]bool{
	"-cp": true, "-classpath": true, "--class-path": true,
	"-p": true, "--module-path": true, "--upgrade-module-path": true,
	"--add-modules": true, "--limit-modules": true, "--add-opens": true,
	"--add-exports": true, "--add-reads": true, "--patch-module": true,
	"--enable-native-access": true,
}

func (l *Launch) parseJava(args []string) {
	l.Runtime = "Java"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-jar":
			if i+1 < len(args) {
				l.Main, l.MainKind = args[i+1], "jar"
				l.AppArgs = args[i+2:]
			}
			l.hintJava()
			return
		case a == "-m" || a == "--module":
			if i+1 < len(args) {
				l.Main, l.MainKind = args[i+1], "module"
				l.AppArgs = args[i+2:]
			}
			l.hintJava()
			return
		case strings.HasPrefix(a, "--module="):
			l.Main, l.MainKind = strings.TrimPrefix(a, "--module="), "module"
			l.AppArgs = args[i+1:]
			l.hintJava()
			return
		case a == "-cp" || a == "-classpath" || a == "--class-path":
			if i+1 < len(args) {
				l.Classpath = strings.Split(args[i+1], ":")
				i++
			}
		case strings.HasPrefix(a, "--class-path="):
			l.Classpath = strings.Split(strings.TrimPrefix(a, "--class-path="), ":")
		case strings.HasPrefix(a, "-D"):
			k, v, _ := strings.Cut(a[2:], "=")
			l.Props = append(l.Props, [2]string{k, v})
		case strings.HasPrefix(a, "@"):
			l.Options = append(l.Options, a+" (arguments file)")
		case strings.HasPrefix(a, "-"):
			if javaTakesValue[a] && i+1 < len(args) {
				l.Options = append(l.Options, a+" "+args[i+1])
				i++
			} else {
				l.Options = append(l.Options, a)
			}
		default:
			l.Main, l.MainKind = a, "class"
			l.AppArgs = args[i+1:]
			l.hintJava()
			return
		}
	}
	l.hintJava()
}

var javaMainHints = map[string]string{
	"org.gradle.launcher.daemon.bootstrap.GradleDaemon": "Gradle build daemon: keeps Gradle warm between builds.",
	"org.gradle.wrapper.GradleWrapperMain":              "Gradle wrapper (./gradlew) running a build.",
	"org.codehaus.plexus.classworlds.launcher.Launcher": "Apache Maven running a build.",
	"org.apache.maven.wrapper.MavenWrapperMain":         "Maven wrapper (./mvnw) running a build.",
	"com.intellij.idea.Main":                            "IntelliJ IDEA (JetBrains IDE).",
	"org.jetbrains.kotlin.daemon.KotlinCompileDaemon":   "Kotlin compile daemon: speeds up Kotlin builds.",
	"org.eclipse.equinox.launcher.Main":                 "Eclipse IDE / Eclipse-based language server.",
	"org.elasticsearch.server.cli.ServerCli":            "Elasticsearch search server.",
	"org.apache.zookeeper.server.quorum.QuorumPeerMain": "Apache ZooKeeper server.",
	"kafka.Kafka":                                        "Apache Kafka broker.",
	"org.sonarsource.scanner.cli.Main":                   "SonarScanner code-analysis run.",
	"org.jetbrains.jps.cmdline.Launcher":                 "IntelliJ's external build process.",
	"clojure.main":                                       "Clojure program (clojure.main).",
	"com.martiansoftware.nailgun.NGServer":               "Nailgun server (keeps a JVM warm for command-line tools).",
	"org.apache.catalina.startup.Bootstrap":              "Apache Tomcat web server.",
	"org.springframework.boot.loader.launch.JarLauncher": "Spring Boot application.",
	"org.springframework.boot.loader.JarLauncher":        "Spring Boot application.",
}

func (l *Launch) hintJava() {
	if h, ok := javaMainHints[l.Main]; ok {
		l.Hints = append(l.Hints, h)
	}
	if l.Main == "clojure.main" {
		a := l.AppArgs
		for i := 0; i < len(a); i++ {
			switch a[i] {
			case "-m", "--main":
				if i+1 < len(a) {
					l.Hints = append(l.Hints, "Runs the -main function of the Clojure namespace "+a[i+1]+".")
					l.AppArgs = a[i+2:]
					l.Main = "clojure.main -m " + a[i+1]
					return
				}
			case "-i", "--init", "-e", "--eval", "-r", "--repl":
				l.Hints = append(l.Hints, "Clojure "+strings.TrimLeft(a[i], "-")+" mode.")
			default:
				if !strings.HasPrefix(a[i], "-") {
					l.Hints = append(l.Hints, "Runs the Clojure script "+a[i]+".")
					return
				}
			}
		}
	}
	for _, p := range l.Props {
		switch p[0] {
		case "nrepl.middleware", "clojure.basis":
			l.Hints = append(l.Hints, "Started by the Clojure CLI (deps.edn project); basis file "+p[1]+".")
			return
		}
	}
	if strings.HasPrefix(l.Main, "org.springframework") || strings.Contains(l.Main, "SpringApplication") {
		l.Hints = append(l.Hints, "Spring application.")
	}
}

var scriptRuntimes = map[string]string{
	"node": "Node.js", "nodejs": "Node.js", "deno": "Deno", "bun": "Bun",
	"python": "Python", "ruby": "Ruby", "perl": "Perl",
	"php": "PHP", "bash": "Bash", "sh": "sh", "zsh": "zsh", "dash": "sh",
	"osascript": "AppleScript", "lua": "Lua",
}

func (l *Launch) parseScript(exe string, args []string) {
	// python3.12 -> python, ruby3.3 -> ruby
	name := strings.TrimRight(exe, "0123456789.")
	if _, ok := scriptRuntimes[exe]; ok {
		name = exe
	}
	rt, ok := scriptRuntimes[name]
	if !ok {
		l.AppArgs = args
		return
	}
	l.Runtime = rt
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" && name == "python" && i+1 < len(args):
			l.Main, l.MainKind = args[i+1], "module"
			l.AppArgs = args[i+2:]
			return
		case a == "-e" || a == "-c" || a == "--eval" || a == "-r":
			l.MainKind = "inline"
			l.Main = "(inline code via " + a + ")"
			return
		case strings.HasPrefix(a, "-"):
			l.Options = append(l.Options, a)
		default:
			l.Main, l.MainKind = a, "script"
			l.AppArgs = args[i+1:]
			return
		}
	}
}

// ReadJarManifest pulls identifying attributes out of a jar.
func ReadJarManifest(path string) map[string]string {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "META-INF/MANIFEST.MF" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		data, _ := io.ReadAll(io.LimitReader(rc, 64*1024))
		out := map[string]string{}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		var lastKey string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, " ") && lastKey != "" { // continuation
				out[lastKey] += strings.TrimPrefix(line, " ")
				continue
			}
			k, v, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			switch k {
			case "Main-Class", "Start-Class", "Implementation-Title", "Implementation-Version", "Implementation-Vendor", "Built-By", "Multi-Release":
				out[k] = v
				lastKey = k
			default:
				lastKey = ""
			}
		}
		return out
	}
	return nil
}
