// The search excerpt, as it crosses the wire.
//
// The service HTML-escapes the matched window and wraps the hit term in
// <mark> tags (FTS5 highlight). A card's snippet is plain prose, so it is
// decoded here: the reader sees "&" and not "&amp;", and never the literal
// <mark> tag.
package dev.repotory.jetbrains

/** The tags the service wraps the matched term in. */
private const val MARK_OPEN = "<mark>"
private const val MARK_CLOSE = "</mark>"

/**
 * The excerpt as plain text: the highlight tags gone, the entities decoded,
 * the whitespace flat.
 *
 * The result row is a plain label, so it cannot colour the matched word —
 * what it can do is show readable prose instead of the raw
 * "<mark>repotory</mark>" the service sends.
 */
internal fun snippetPlain(snippet: String, max: Int = 140): String {
    val flat = decodeEntities(stripMarks(snippet))
        .replace(Regex("\\s+"), " ")
        .trim()
    return if (flat.length <= max) flat else flat.take(max - 1) + "…"
}

/** The excerpt without the service's highlight tags. */
private fun stripMarks(snippet: String): String =
    snippet.replace(MARK_OPEN, "").replace(MARK_CLOSE, "")

/**
 * The service escapes the note text before it crosses the wire, so the
 * excerpt arrives with entities in it. Decoding once here keeps an "&" in a
 * note reading as "&" instead of "&amp;".
 */
private fun decodeEntities(text: String): String =
    ENTITY.replace(text) { m ->
        val body = m.groupValues[1]
        when {
            body.startsWith("#x") || body.startsWith("#X") ->
                body.drop(2).toIntOrNull(16)?.toChar()?.toString() ?: m.value
            body.startsWith("#") ->
                body.drop(1).toIntOrNull()?.toChar()?.toString() ?: m.value
            else -> when (body) {
                "amp" -> "&"
                "lt" -> "<"
                "gt" -> ">"
                "quot" -> "\""
                "apos" -> "'"
                "nbsp" -> " "
                else -> m.value
            }
        }
    }

private val ENTITY = Regex("&(#x?[0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);")
