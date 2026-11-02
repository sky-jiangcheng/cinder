package dev.repotory.jetbrains

import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/**
 * The regression suite for the MCP stdio client (ADR-0035 decision 2).
 *
 * The client is exercised against a FAKE server written in Java and run
 * through the single-file source launcher (`java FakeMcpServer.java`), so the
 * test needs nothing but a JDK — no Go toolchain, no shell, no network. What
 * it proves is the part unit tests over the parsers cannot: that the
 * handshake happens, that the request and the answer find each other, and
 * that a server-side error reaches the caller as an error.
 */
class McpStdioClientTest {
    private fun javaBin(): String = File(System.getProperty("java.home"), "bin/java").path

    private fun fakeServer(): File {
        val dir = Files.createTempDirectory("repotory-fake-mcp").toFile()
        dir.deleteOnExit()
        val file = File(dir, "FakeMcpServer.java")
        file.writeText(FAKE_SERVER_SOURCE)
        file.deleteOnExit()
        return file
    }

    private fun payloadFile(text: String): File {
        val file = Files.createTempFile("repotory-fake-payload", ".txt").toFile()
        file.writeText(text)
        file.deleteOnExit()
        return file
    }

    @Test
    fun `the handshake completes and a tool call round trips`() {
        val payload = """[{"type":"note","id":42,"project_id":7,"title":"t","snippet":"s"}]"""
        val client = McpStdioClient(
            command = javaBin(),
            args = listOf(fakeServer().path, payloadFile(payload).path),
        )
        try {
            client.start()
            assertTrue(client.isRunning)
            val text = client.callTool("repotory_notes_search", com.google.gson.JsonObject())
            assertEquals(payload, text)
        } finally {
            client.close()
        }
    }

    @Test
    fun `a server-side error reaches the caller as an error`() {
        val payload = "[]"
        val error = payloadFile("repotory-mcp refused the request")
        val client = McpStdioClient(
            command = javaBin(),
            args = listOf(fakeServer().path, payloadFile(payload).path, error.path),
        )
        try {
            client.start()
            val failure = assertFailsWith<McpException> {
                client.callTool("repotory_notes_search", com.google.gson.JsonObject())
            }
            assertTrue(failure.message!!.contains("refused the request"))
        } finally {
            client.close()
        }
    }

    @Test
    fun `a missing binary fails with a message that says so`() {
        val client = McpStdioClient(command = "/definitely/not/here/repotory-mcp")
        val failure = assertFailsWith<McpException> { client.start() }
        assertTrue(failure.message!!.contains("Cannot start"))
    }

    /**
     * The fake server.
     *
     * It speaks just enough of the protocol: the `initialize` handshake and
     * `tools/call`. The first argument is a file holding the text the tool
     * answers with; the optional second argument holds an error message, in
     * which case `tools/call` fails the way a refusing server would.
     */
    private companion object {
        val FAKE_SERVER_SOURCE = """
            import java.io.BufferedReader;
            import java.io.InputStreamReader;
            import java.io.OutputStreamWriter;
            import java.io.PrintWriter;
            import java.nio.charset.StandardCharsets;
            import java.nio.file.Files;
            import java.nio.file.Paths;

            public class FakeMcpServer {
                public static void main(String[] args) throws Exception {
                    BufferedReader in = new BufferedReader(
                            new InputStreamReader(System.in, StandardCharsets.UTF_8));
                    PrintWriter out = new PrintWriter(
                            new OutputStreamWriter(System.out, StandardCharsets.UTF_8), true);
                    // The source file is NOT one of the program's arguments:
                    // `java FakeMcpServer.java a b` hands the program [a, b].
                    String text = args.length > 0 ? read(args[0]) : "";
                    String error = args.length > 1 ? read(args[1]) : "";
                    String line;
                    while ((line = in.readLine()) != null) {
                        String id = idOf(line);
                        if (line.contains("\"method\":\"initialize\"")) {
                            out.println("{\"jsonrpc\":\"2.0\",\"id\":" + id
                                    + ",\"result\":{\"protocolVersion\":\"2024-11-05\","
                                    + "\"capabilities\":{},"
                                    + "\"serverInfo\":{\"name\":\"fake\",\"version\":\"0.0.0\"}}}");
                        } else if (line.contains("\"method\":\"tools/call\"")) {
                            if (!error.isEmpty()) {
                                out.println("{\"jsonrpc\":\"2.0\",\"id\":" + id
                                        + ",\"error\":{\"code\":-32000,\"message\":" + quote(error) + "}}");
                            } else {
                                out.println("{\"jsonrpc\":\"2.0\",\"id\":" + id
                                        + ",\"result\":{\"content\":[{\"type\":\"text\",\"text\":"
                                        + quote(text) + "}]}}");
                            }
                        }
                    }
                }

                private static String read(String path) throws Exception {
                    return new String(Files.readAllBytes(Paths.get(path)), StandardCharsets.UTF_8);
                }

                /** The request id: the first numeric "id" in the line. */
                private static String idOf(String line) {
                    int at = line.indexOf("\"id\":");
                    if (at < 0) return "0";
                    int i = at + 5;
                    StringBuilder digits = new StringBuilder();
                    while (i < line.length() && Character.isDigit(line.charAt(i))) {
                        digits.append(line.charAt(i));
                        i++;
                    }
                    return digits.length() == 0 ? "0" : digits.toString();
                }

                /** A JSON string literal, escaped. */
                private static String quote(String value) {
                    StringBuilder sb = new StringBuilder("\"");
                    for (char c : value.toCharArray()) {
                        switch (c) {
                            case '"': sb.append("\\\""); break;
                            case '\\': sb.append("\\\\"); break;
                            case '\n': sb.append("\\n"); break;
                            case '\r': sb.append("\\r"); break;
                            case '\t': sb.append("\\t"); break;
                            default:
                                if (c < 0x20) {
                                    sb.append(String.format("\\u%04x", (int) c));
                                } else {
                                    sb.append(c);
                                }
                        }
                    }
                    return sb.append('"').toString();
                }
            }
        """.trimIndent()
    }
}
