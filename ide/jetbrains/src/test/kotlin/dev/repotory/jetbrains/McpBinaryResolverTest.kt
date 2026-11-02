package dev.repotory.jetbrains

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * The regression suite for the binary lookup (ADR-0035 decision 7).
 *
 * The order matters and is worth pinning: setting -> `REPOTORY_MCP_BIN` ->
 * PATH -> the package-manager directories. The subtle case is the one that
 * must NOT fall through — a setting that points at a file which is not there
 * is a mistake the user needs to see, not something to quietly route around.
 *
 * Every case is hermetic: the directories handed to the resolver are
 * throwaway ones, so a machine that HAPPENS to have repotory-mcp installed
 * (a developer box, a CI image) cannot change the outcome.
 */
class McpBinaryResolverTest {
    /** A throwaway directory holding an executable named `repotory-mcp`. */
    private fun installDir(name: String): File {
        val dir = File.createTempFile(name, ".dir").let {
            it.delete()
            it.mkdirs()
            it.deleteOnExit()
            it
        }
        val binary = File(dir, "repotory-mcp")
        binary.writeText("#!/bin/sh\nexit 0\n")
        binary.setExecutable(true)
        binary.deleteOnExit()
        return dir
    }

    @Test
    fun `the setting wins over everything else`() {
        val chosen = File(installDir("chosen"), "repotory-mcp")
        val other = File(installDir("other"), "repotory-mcp")
        val resolution = McpBinaryResolver.resolve(
            configured = chosen.path,
            envValue = other.path,
            pathDirs = listOf(other.parent),
            commonDirs = emptyList(),
            home = "/tmp/home",
            windows = false,
        )
        assertTrue(resolution.found)
        assertEquals(chosen.path, resolution.path)
        assertEquals("setting", resolution.source)
    }

    @Test
    fun `a configured path that is not there is a problem, not a fall-through`() {
        // The user pointed at a file. Pretending otherwise — by silently
        // using some other binary — would hide the mistake.
        val onPath = File(installDir("onpath"), "repotory-mcp")
        val resolution = McpBinaryResolver.resolve(
            configured = "/tmp/definitely-not-here/repotory-mcp",
            envValue = null,
            pathDirs = listOf(onPath.parent),
            commonDirs = emptyList(),
            home = "/tmp/home",
            windows = false,
        )
        assertNull(resolution.path)
        assertNotNull(resolution.problem)
        assertTrue(resolution.problem!!.contains("/tmp/definitely-not-here/repotory-mcp"))
    }

    @Test
    fun `the environment variable is used when no setting is given`() {
        val fromEnv = File(installDir("env"), "repotory-mcp")
        val resolution = McpBinaryResolver.resolve(
            configured = "  ",
            envValue = fromEnv.path,
            pathDirs = emptyList(),
            commonDirs = emptyList(),
            home = "/tmp/home",
            windows = false,
        )
        assertTrue(resolution.found)
        assertEquals(fromEnv.path, resolution.path)
        assertEquals("env", resolution.source)
    }

    @Test
    fun `PATH is searched before the common directories`() {
        val onPath = File(installDir("path"), "repotory-mcp")
        val inCommon = File(installDir("common"), "repotory-mcp")
        val resolution = McpBinaryResolver.resolve(
            configured = null,
            envValue = null,
            pathDirs = listOf(onPath.parent),
            commonDirs = listOf(inCommon.parent),
            home = "/tmp/home",
            windows = false,
        )
        assertTrue(resolution.found)
        assertEquals(onPath.path, resolution.path)
        assertEquals("path", resolution.source)
    }

    @Test
    fun `the common directories are searched too`() {
        // A binary that is NOT on PATH but IS in a package-manager directory
        // (Homebrew, Scoop, ~/.local/bin) is the normal install.
        val inCommon = File(installDir("commononly"), "repotory-mcp")
        val resolution = McpBinaryResolver.resolve(
            configured = null,
            envValue = null,
            pathDirs = emptyList(),
            commonDirs = listOf(inCommon.parent),
            home = "/tmp/home",
            windows = false,
        )
        assertTrue(resolution.found, "expected the common-directory copy to be found")
        assertEquals(inCommon.path, resolution.path)
        assertEquals("common", resolution.source)
    }

    @Test
    fun `a missing binary reports every place it looked`() {
        val resolution = McpBinaryResolver.resolve(
            configured = null,
            envValue = null,
            pathDirs = listOf("/tmp/no-such-dir"),
            commonDirs = emptyList(),
            home = "/tmp/home",
            windows = false,
        )
        assertNull(resolution.path)
        assertNotNull(resolution.problem)
        // The diagnostics are the actionable part: a user who cannot find
        // their binary needs the LIST, not just the verdict.
        assertTrue(resolution.attempts.any { it.contains("/tmp/no-such-dir") })
        assertTrue(resolution.attempts.any { it.endsWith("repotory-mcp") })
    }

    @Test
    fun `a tilde in the setting is expanded`() {
        val dir = installDir("tilde")
        val resolution = McpBinaryResolver.resolve(
            configured = "~/repotory-mcp",
            envValue = null,
            pathDirs = emptyList(),
            commonDirs = emptyList(),
            home = dir.parent,
            windows = false,
        )
        // The home directory does not hold the binary, so the resolution
        // fails — but it fails with the EXPANDED path, which is what the
        // diagnostic has to show.
        assertNull(resolution.path)
        assertTrue(resolution.attempts.any { it.startsWith(dir.parent) })
    }
}
