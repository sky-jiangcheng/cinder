// One-click local service start for the JetBrains plugin.
//
// ADR-0027 decision 4 used to say "connect only": the plugin never starts a
// server. That rule assumed the user already runs one, which made the plugin
// unusable out of the box — a first-time user had to build the server by hand,
// remember --web-dist, and keep a terminal open. The 2026-10-22 usability
// revision (same ADR) changes the rule: the plugin MAY start a loopback
// repotory-server, but only
//   · when nothing already answers /health (never fight a running instance),
//   · from a binary the user already has (setting / env / PATH / common dirs;
//     no download, no build, no package manager),
//   · on a loopback URL only (a remote address is not ours to launch),
//   · and it stops only the process it started itself.
//
// The thin-client discipline is untouched: this file only spawns a local
// process; every answer still comes from the shared service layer over HTTP.
package dev.repotory.jetbrains

import com.intellij.ide.BrowserUtil
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.service
import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.fileChooser.FileChooser
import com.intellij.openapi.fileChooser.FileChooserDescriptor
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.util.Disposer
import java.io.File
import java.net.URI
import java.util.concurrent.TimeUnit

private const val LOG = "dev.repotory.jetbrains"
private const val SERVER_ENV = "REPOTORY_SERVER_BIN"

// How long to wait for /health after spawning the server.
//
// It used to be five seconds, which is short for a cold start: opening the
// knowledge base runs the schema re-assertions and, when the desktop app
// happens to hold the write lock, the server retries for a few seconds
// before it can answer at all. Five seconds was the timeout, not the
// reality — the panel then reported "started" for a server that was not up
// yet, and the search that followed failed with "cannot reach".
private const val START_TIMEOUT_MS = 30_000L

/** How often the health probe is repeated while waiting for the server. */
private const val HEALTH_POLL_MS = 250L

private const val RELEASES_URL = "https://github.com/sky-jiangcheng/repotory/releases"

/** Where a resolved server binary came from, for the settings/diagnostics text. */
data class ServerBinary(val file: File, val source: String)

/** Result of [RepotoryServerManager.start]; never a bare Boolean. */
sealed interface StartOutcome {
    /**
     * A server owned by this plugin is now answering (or is about to).
     *
     * [healthy] says whether /health answered within the wait. A process that
     * is up but silent is NOT the same as a working service, and the caller
     * has to be able to tell the two apart — otherwise "started" is a promise
     * the next search breaks.
     */
    data class Started(val port: Int, val webUi: Boolean, val binary: ServerBinary, val healthy: Boolean = true) : StartOutcome

    /** Something already answered /health; the plugin did not spawn anything. */
    data object AlreadyRunning : StartOutcome

    /** No executable was found in any of the documented locations. */
    data class BinaryMissing(val searched: String) : StartOutcome

    /** The spawn or the health wait failed; [message] is user-presentable. */
    data class Failed(val message: String) : StartOutcome
}

/** Expands a leading `~` so a path copied from a shell setting works verbatim. */
fun expandHome(value: String): File {
    val home = System.getProperty("user.home") ?: return File(value)
    return when {
        value == "~" -> File(home)
        value.startsWith("~/") || value.startsWith("~\\") -> File(home, value.substring(2))
        else -> File(value)
    }
}

/**
 * Owns the optional `repotory-server` child process.
 *
 * Everything here is blocking with deliberately short timeouts; callers run it
 * on a pooled thread (the EDT must never wait on a process or a socket).
 */
@Service(Service.Level.APP)
class RepotoryServerManager {
    private var process: Process? = null
    private var ownedByPlugin = false

    init {
        // Stop only our own child when the IDE exits. A server the user started
        // themselves is not ours to kill, and the flag below keeps that true.
        Disposer.register(
            ApplicationManager.getApplication(),
            object : com.intellij.openapi.Disposable {
                override fun dispose() = stopOwnedServer()
            },
        )
    }

    private fun isWindows(): Boolean =
        System.getProperty("os.name").orEmpty().lowercase().contains("win")

    private fun binaryName(): String = if (isWindows()) "repotory-server.exe" else "repotory-server"

    /** Documented resolution order; the first existing file wins. */
    fun resolveBinary(override: File? = null): ServerBinary? {
        if (override != null && override.isFile) return ServerBinary(override, "selected file")

        val configured = RepotorySettings.serverBinaryPath().takeIf { it.isNotBlank() }
        if (configured != null) {
            val file = expandHome(configured)
            if (file.isFile) return ServerBinary(file, "setting")
        }

        val fromEnv = System.getenv(SERVER_ENV)?.takeIf { it.isNotBlank() }
        if (fromEnv != null) {
            val file = expandHome(fromEnv)
            if (file.isFile) return ServerBinary(file, "environment ($SERVER_ENV)")
        }

        for (dir in pathDirs() + commonDirs()) {
            val file = File(dir, binaryName())
            if (file.isFile && file.canExecute()) return ServerBinary(file, dir.absolutePath)
        }
        return null
    }

    /** Human-readable list for the "binary missing" dialog. */
    fun searchedLocations(): String =
        buildString {
            append("setting (`repotory.serverBinaryPath`)\n")
            append("environment (`$SERVER_ENV`)\n")
            append("PATH\n")
            append(commonDirs().joinToString("\n") { it.absolutePath })
        }

    /**
     * Starts a loopback server for [serverUrl], or reports why it could not.
     *
     * Blocking: waits up to [START_TIMEOUT_MS] for `/health` after spawning.
     * [onProgress] receives short human-readable lines ("Launching …", "Waiting
     * for the service …") so the panel can narrate a start that takes seconds
     * instead of looking frozen.
     */
    fun start(
        serverUrl: String,
        binaryOverride: File? = null,
        onProgress: ((String) -> Unit)? = null,
    ): StartOutcome {
        if (repotoryClient.ping(serverUrl)) return StartOutcome.AlreadyRunning

        val uri = try {
            URI.create(serverUrl.trimEnd('/'))
        } catch (e: Exception) {
            return StartOutcome.Failed("Invalid server address: $serverUrl")
        }
        if (!isLoopback(uri.host)) {
            return StartOutcome.Failed(
                "The plugin only starts a LOCAL service; $serverUrl is not loopback. " +
                    "Start that server yourself and use Test connection.",
            )
        }

        val binary = resolveBinary(binaryOverride)
            ?: return StartOutcome.BinaryMissing(searchedLocations())

        val port = if (uri.port > 0) uri.port else 18765
        val command = mutableListOf(binary.file.absolutePath, "--port", port.toString())
        val webDist = File(binary.file.parentFile ?: File("."), "web")
        val webUi = File(webDist, "index.html").isFile
        if (webUi) {
            // The release bundle lays the server binary next to `web/`, so the
            // deep links work without the user passing --web-dist by hand.
            command += listOf("--web-dist", webDist.absolutePath)
        }

        return try {
            val logFile = File(com.intellij.openapi.application.PathManager.getLogPath(), "repotory-server.log")
            logFile.parentFile?.mkdirs()
            val child = ProcessBuilder(command)
                .redirectErrorStream(true)
                .redirectOutput(ProcessBuilder.Redirect.appendTo(logFile))
                .start()
            process = child
            ownedByPlugin = true

            onProgress?.invoke("Launched ${binary.file.name} — waiting for the service…")
            val deadline = System.currentTimeMillis() + START_TIMEOUT_MS
            while (System.currentTimeMillis() < deadline) {
                if (repotoryClient.ping(serverUrl)) {
                    onProgress?.invoke("Service is up.")
                    return StartOutcome.Started(port, webUi, binary, healthy = true)
                }
                if (!child.isAlive) {
                    val tail = logTail(logFile)
                    // "address already in use" is the single most common
                    // failure here: the user has a server on that port that
                    // the plugin cannot reach (a stale binary, a different
                    // interface, a firewall). Saying so is worth more than
                    // the exit code.
                    if (tail.contains("address already in use", ignoreCase = true)) {
                        return StartOutcome.Failed(
                            "Port $port is already taken by another process, so the service " +
                                "never started. Stop that process (or pick another port) and try again.",
                        )
                    }
                    return StartOutcome.Failed(
                        "repotory-server exited during startup (code ${child.exitValue()}). " +
                            (if (tail.isBlank()) "" else "Last output: $tail") +
                            " Log: ${logFile.absolutePath}.",
                    )
                }
                Thread.sleep(HEALTH_POLL_MS)
            }
            // The process is alive but /health never answered. That is not a
            // working service: say so instead of handing back a promise the
            // next search would break.
            onProgress?.invoke("The process is running but the service is not answering yet.")
            StartOutcome.Started(port, webUi, binary, healthy = false)
        } catch (e: Exception) {
            Logger.getInstance(LOG).warn("repotory-server start failed", e)
            StartOutcome.Failed("Could not start ${binary.file.absolutePath}: ${e.message ?: e.javaClass.simpleName}")
        }
    }

    /**
     * The last few meaningful lines of the server log.
     *
     * The log is appended to across starts and can grow without bound, so
     * only its tail is read — the last few kilobytes are enough to see the
     * line that killed the process.
     */
    private fun logTail(logFile: File, maxLines: Int = 4): String {
        if (!logFile.isFile) return ""
        return try {
            val bytes = logFile.length()
            val window = minOf(bytes, 4096L).toInt()
            val text = java.io.RandomAccessFile(logFile, "r").use { raf ->
                raf.seek(bytes - window)
                String(ByteArray(window).also { raf.readFully(it) }, Charsets.UTF_8)
            }
            text.lines()
                .map { it.trim() }
                .filter { it.isNotBlank() }
                .takeLast(maxLines)
                .joinToString(" / ")
        } catch (e: Exception) {
            ""
        }
    }

    /** Kills the child only when this plugin spawned it. */
    fun stopOwnedServer() {
        val child = process ?: return
        if (!ownedByPlugin) return
        ownedByPlugin = false
        process = null
        try {
            if (child.isAlive) {
                child.destroy()
                if (!child.waitFor(2, TimeUnit.SECONDS)) child.destroyForcibly()
            }
        } catch (e: Exception) {
            Logger.getInstance(LOG).debug("repotory-server stop failed: ${e.message}")
        }
    }

    private fun isLoopback(host: String?): Boolean =
        host == null || host == "127.0.0.1" || host == "localhost" || host == "::1" || host == "[::1]"

    private fun pathDirs(): List<File> =
        System.getenv("PATH").orEmpty()
            .split(File.pathSeparatorChar)
            .mapNotNull { it.takeIf { p -> p.isNotBlank() }?.let(::File) }
            .filter { it.isDirectory }

    private fun commonDirs(): List<File> {
        val home = File(System.getProperty("user.home").orEmpty())
        return if (isWindows()) {
            val local = System.getenv("LOCALAPPDATA")?.let(::File) ?: File(home, "AppData/Local")
            listOf(
                File(home, "scoop/shims"),
                File(local, "Programs/repotory"),
                File(local, "repotory"),
            )
        } else {
            listOf(
                File("/opt/homebrew/bin"),
                File("/usr/local/bin"),
                File("/usr/bin"),
                File(home, ".local/bin"),
                File("/home/linuxbrew/.linuxbrew/bin"),
            )
        }
    }
}

/** App-level accessor, matching the other services in this plugin. */
val repotoryServer: RepotoryServerManager
    get() = service()

/** File picker shared by the settings page and the tool window. */
fun chooseServerBinary(): File? {
    // Files only (not directories), and the dialog may also reveal them in the
    // editor: the plugin needs an existing executable, never a folder.
    val descriptor = FileChooserDescriptor(true, false, false, false, false, false)
        .withTitle("Select the repotory-server executable")
    val chosen = FileChooser.chooseFile(descriptor, null, null) ?: return null
    return File(chosen.path)
}

/** The install guidance shown when no binary was found; returns the user's choice. */
fun offerServerBinaryHelp(searched: String): Boolean {
    val choice = Messages.showDialog(
        "No repotory-server executable was found. Looked in:\n\n$searched\n\n" +
            "Download the release bundle (the binary plus its web UI) and point the plugin at it, " +
            "or set repotory.serverBinaryPath in Settings | Tools | Repotory.",
        "Repotory: local service binary not found",
        arrayOf("Set Path…", "Open Download Page", "Cancel"),
        0,
        Messages.getWarningIcon(),
    )
    return when (choice) {
        0 -> {
            val chosen = chooseServerBinary()
            if (chosen != null) {
                RepotorySettings.setServerBinaryPath(chosen.absolutePath)
                true
            } else {
                false
            }
        }
        1 -> {
            BrowserUtil.browse(RELEASES_URL)
            false
        }
        else -> false
    }
}

/**
 * File picker for the `repotory-mcp` executable.
 *
 * The plugin never downloads it (ADR-0035 decision 7): `repotory-mcp` has
 * three distribution channels already (install.sh, Homebrew, Scoop), and the
 * plugin's job is to find the one the user installed.
 */
fun chooseMcpBinary(): File? {
    // Files only (not directories): the plugin needs an existing executable.
    val descriptor = FileChooserDescriptor(true, false, false, false, false, false)
        .withTitle("Select the repotory-mcp executable")
    val chosen = FileChooser.chooseFile(descriptor, null, null) ?: return null
    return File(chosen.path)
}

