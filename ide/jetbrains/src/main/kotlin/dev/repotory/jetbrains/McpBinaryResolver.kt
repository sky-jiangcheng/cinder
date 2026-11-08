// Cinder for JetBrains — where the `cinder-mcp` binary is.
//
// ADR-0035 decision 7: the plugin resolves the binary the same way the VS
// extension does — setting -> `REPOTORY_MCP_BIN` -> PATH -> the common
// package-manager directories — and the plugin NEVER downloads it. Three
// distribution channels already exist for `cinder-mcp` (install.sh, Homebrew,
// Scoop); the plugin's job is to find the one the user already installed.
//
// A configured-but-missing path is reported as a problem instead of falling
// through to PATH: the user pointed at a file, and pretending otherwise would
// hide the mistake behind some other binary.
//
// The resolution is a pure function of its inputs (no IntelliJ, no ambient
// state) so the order and the diagnostics can be tested on any platform.
package dev.repotory.jetbrains

import java.io.File

/**
 * The outcome of looking for the binary.
 *
 * [attempts] lists every place that was tried, in order — the diagnostics
 * the settings page shows when nothing was found. A user who cannot find
 * their binary needs the LIST, not just the verdict.
 */
data class McpBinaryResolution(
    /** The absolute path of the executable, when one was found. */
    val path: String? = null,
    /** Where it came from: setting | env | path | common. */
    val source: String = "",
    /** Why it could not be resolved, when it could not. */
    val problem: String? = null,
    /** Every location that was tried, in order. */
    val attempts: List<String> = emptyList(),
) {
    val found: Boolean get() = path != null
}

object McpBinaryResolver {
    const val ENV_VAR = "REPOTORY_MCP_BIN"

    /**
     * The binary's name on each platform; Windows carries the extension.
     *
     * `cinder-mcp` first, `repotory-mcp` second: the rename kept the old name
     * as a symlink on every install (ADR-0049 补注), so a machine that has not
     * been updated yet still resolves.
     */
    val BINARY_NAMES = listOf("cinder-mcp", "cinder-mcp.exe", "repotory-mcp", "repotory-mcp.exe")

    /**
     * Resolve the binary.
     *
     * @param configured the settings value, blank when unset
     * @param envValue the `REPOTORY_MCP_BIN` value, blank when unset
     * @param pathDirs the directories on PATH, in order
     * @param commonDirs the package-manager directories, in order
     * @param home the user's home directory
     * @param windows whether this is Windows (picks the executable name)
     */
    fun resolve(
        configured: String?,
        envValue: String?,
        pathDirs: List<String>,
        commonDirs: List<String>,
        home: String,
        windows: Boolean,
    ): McpBinaryResolution {
        val attempts = mutableListOf<String>()
        val names = if (windows) listOf("cinder-mcp.exe", "repotory-mcp.exe")
        else listOf("cinder-mcp", "repotory-mcp")

        // 1. The setting. Explicit wins, and a broken explicit path is an
        //    error to report — not something to route around.
        val configuredPath = configured?.trim().orEmpty()
        if (configuredPath.isNotEmpty()) {
            val file = expandHome(configuredPath, home)
            attempts += file.path
            if (file.isFile && file.canExecute()) {
                return McpBinaryResolution(file.path, "setting", attempts = attempts)
            }
            return McpBinaryResolution(
                problem = "The configured cinder-mcp path is not an executable file: $file",
                attempts = attempts,
            )
        }

        // 2. The environment variable.
        val envPath = envValue?.trim().orEmpty()
        if (envPath.isNotEmpty()) {
            val file = expandHome(envPath, home)
            attempts += file.path
            if (file.isFile && file.canExecute()) {
                return McpBinaryResolution(file.path, "env", attempts = attempts)
            }
            return McpBinaryResolution(
                problem = "The $ENV_VAR environment variable points at a file that is not executable: $file",
                attempts = attempts,
            )
        }

        // 3. PATH, then 4. the common package-manager directories.
        val seen = mutableSetOf<String>()
        for (dir in pathDirs + commonDirs) {
            if (dir.isBlank() || !seen.add(dir)) continue
            for (name in names) {
                val candidate = File(dir, name)
                attempts += candidate.path
                if (candidate.isFile && candidate.canExecute()) {
                    val source = if (dir in commonDirs) "common" else "path"
                    return McpBinaryResolution(candidate.path, source, attempts = attempts)
                }
            }
        }
        return McpBinaryResolution(
            problem = "cinder-mcp was not found in PATH, the common install directories, " +
                "or the settings — install it (install.sh, Homebrew or Scoop) or point the setting at it",
            attempts = attempts,
        )
    }

    /**
     * The package-manager locations the binary is installed into.
     *
     * The same list the VS Code extension searches (ide/vscode/src/
     * extension.ts): Homebrew on macOS, Scoop on Windows, the usual Linux
     * locations otherwise.
     */
    fun commonBinaryDirs(home: String, windows: Boolean): List<String> {
        if (windows) {
            val local = System.getenv("LOCALAPPDATA") ?: File(home, "AppData/Local").path
            return listOf(
                File(local, "scoop/shims").path,
                File(local, "scoop/apps/cinder-mcp/current").path,
                File(local, "scoop/apps/repotory-mcp/current").path,
                File(local, "Programs/cinder-mcp").path,
                File(local, "Programs/repotory-mcp").path,
                File(local, "cinder-mcp").path,
                File(local, "repotory-mcp").path,
            )
        }
        val isMac = System.getProperty("os.name", "").lowercase().contains("mac")
        return if (isMac) {
            listOf(
                "/opt/homebrew/bin",
                "/usr/local/bin",
                "/usr/local/opt/cinder-mcp/bin",
                "/usr/local/opt/repotory-mcp/bin",
                File(home, ".local/bin").path,
            )
        } else {
            listOf(
                "/usr/local/bin",
                "/usr/bin",
                File(home, ".local/bin").path,
                "/home/linuxbrew/.linuxbrew/bin",
            )
        }
    }

    /** Expand a leading `~` so a path copied from a shell works verbatim. */
    fun expandHome(value: String, home: String): File = when {
        value == "~" -> File(home)
        value.startsWith("~/") || value.startsWith("~\\") -> File(home, value.substring(2))
        else -> File(value)
    }

    /** Resolve against the real machine. */
    fun resolveHere(configured: String?): McpBinaryResolution {
        val home = System.getProperty("user.home") ?: ""
        val windows = System.getProperty("os.name", "").lowercase().contains("win")
        return resolve(
            configured = configured,
            envValue = System.getenv(ENV_VAR),
            pathDirs = System.getenv("PATH").orEmpty().split(File.pathSeparator),
            commonDirs = commonBinaryDirs(home, windows),
            home = home,
            windows = windows,
        )
    }
}
