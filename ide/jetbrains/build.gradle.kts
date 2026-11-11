// Cinder for JetBrains — Gradle build.
//
// ADR-0027 decision 3: Kotlin + Gradle (IntelliJ Platform Plugin), an
// INDEPENDENT code base. The double maintenance surface ADR-0009 warned about
// is knowingly accepted, with the thin-client discipline pressing the
// maintenance cost down to the API layer.
//
// The IntelliJ Platform Gradle plugin (2.x) is the standard toolchain for
// building a plugin that installs into any IntelliJ-family IDE.
import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType

plugins {
    id("java")
    id("org.jetbrains.intellij.platform") version "2.1.0"
    // Kotlin 2.2.x is required by IDEA 2025.2: the platform's own classes are
    // compiled with Kotlin 2.2.0 metadata, and an older compiler aborts with
    // "Module was compiled with an incompatible version of Kotlin" plus an
    // internal compiler error. The compiler must be at least as new as the
    // metadata it reads.
    id("org.jetbrains.kotlin.jvm") version "2.2.0"
}

group = "dev.repotory"
// The plugin deliberately follows the app version (wails.json -> 2.10.0) rather
// than keeping a line of its own. It used to say 0.1.0, which would have shipped
// a Marketplace build whose number said nothing about the service it talks to;
// the VS Code extension learned the same lesson (scripts/bump-version.sh, 2b).
// One version, one CHANGELOG (ADR-0017 criterion 3). Rewritten by
// scripts/bump-version.sh — do not edit by hand.
version = "2.35.5"

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        // Platform-level only (ADR-0027 decision 1: "com.intellij.modules.platform",
        // IDEA CE first but the same build installs into the whole family).
        // The plugin uses no product-specific API, so there is no per-product
        // dependency to fork.
        create("IC", "2025.2")
        instrumentationTools()
        // The Plugin Verifier CLI is what `verifyPlugin` runs: it checks the
        // built plugin against a real IDE for API misuse and for binary
        // compatibility with the declared since/until range. Without it the
        // task silently has nothing to run.
        pluginVerifier()
    }

    // Plain JVM unit tests (ADR-0035): the MCP protocol client and the
    // payload parsers are deliberately free of IntelliJ APIs, so they are
    // tested WITHOUT the platform's test fixtures — no IDE is downloaded for
    // a test that only needs a JSON-RPC round trip.
    testImplementation(kotlin("test"))
}

intellijPlatform {
    // 242 = 2024.2+, covering the owner's 2024.3 and current 2025.2 with
    // headroom (2026-10-22 decision 1).
    //
    // The verifier warns that sinceBuild (242) is lower than the platform it
    // is built against (252). That gap is the POINT: the build target is the
    // newest IDE the owner uses, while the declared floor is what the plugin
    // claims to support. A plugin built against 252 APIs but declaring 242
    // would be a lie — so the plugin must stay on platform APIs that exist in
    // 242 (it uses only the tool window, the settings page, Swing and
    // java.net), and the Plugin Verifier is what proves that.
    pluginConfiguration {
        ideaVersion {
            sinceBuild = "242"
            // No untilBuild on purpose. The old `243.*` cap meant the plugin
            // declared itself incompatible with every IDE released after
            // 2024.3 — including the 2025.2 it is BUILT against — so the IDE
            // refused to load it on exactly the versions the owner runs.
            // Omitting the upper bound is the correct signal here: the plugin
            // uses only platform APIs that exist in 242 (tool window,
            // settings page, Swing, java.net), so there is no upper bound to
            // guess. If a future platform breaks one of them, the Plugin
            // Verifier is what catches it, not a hard-coded year.
            //
            // `untilBuild = provider { null }` is REQUIRED, not decoration:
            // when the property is left untouched, `patchPluginXml` fills in
            // `<idea-version until-build="<build target>.*"/>` (252.* here),
            // which silently re-caps the plugin at the IDE it was built
            // against. Verified in the packaged zip — without this line the
            // artifact still said until-build="252.*".
            untilBuild = provider { null }
        }
        // Marketplace shows this under "What's new" for every update. A link
        // to the changelog keeps ONE release narrative (ADR-0017 criterion 3)
        // instead of a second copy that would drift from CHANGELOG.md.
        changeNotes = """
            See the changelog for this release:
            https://github.com/sky-jiangcheng/cinder/blob/master/CHANGELOG.md
        """.trimIndent()
    }
    pluginVerification {
        ides {
            // The owner's daily IDE: IntelliJ IDEA CE. Verification against
            // the family is not needed — the plugin declares only platform APIs.
            ide(IntelliJPlatformType.IntellijIdeaCommunity, "2025.2")
        }
    }
    // Publishing to the JetBrains Marketplace (2026-10-22).
    //
    // The token is a permanent Marketplace token from the vendor account.
    // `publishPlugin` uploads the zip built by `buildPlugin`; when the token
    // is absent the task fails, but nothing else in the build is affected —
    // local builds and the GitHub Release asset keep working.
    publishing {
        token = providers.environmentVariable("JETBRAINS_MARKETPLACE_TOKEN")
    }
}

// Signing is configured only when all three secrets exist. Marketplace accepts
// unsigned uploads, so a missing secret degrades to an unsigned release
// instead of failing `publishPlugin`; the signature is what shows the
// "verified vendor" badge, not a gate.
val signingCertificateChain = providers.environmentVariable("CERTIFICATE_CHAIN")
val signingPrivateKey = providers.environmentVariable("PRIVATE_KEY")
val signingPrivateKeyPassword = providers.environmentVariable("PRIVATE_KEY_PASSWORD")
if (signingCertificateChain.isPresent && signingPrivateKey.isPresent && signingPrivateKeyPassword.isPresent) {
    intellijPlatform {
        signing {
            certificateChain = signingCertificateChain
            privateKey = signingPrivateKey
            password = signingPrivateKeyPassword
        }
    }
}

kotlin {
    jvmToolchain(21)
}

tasks {
    buildSearchableOptions {
        // The plugin has no options that need to be indexed at runtime; the
        // search index build adds minutes to every packaging run.
        enabled = false
    }

    // Disabling buildSearchableOptions alone is not enough: the platform
    // plugin's prepareJarSearchableOptions still declares the (now never
    // created) build/tmp/buildSearchableOptions as its INPUT, and Gradle
    // refuses to run a task whose input is missing. On a clean tree that
    // failed `gradle buildPlugin` outright — the CI gate for this plugin
    // was red before it could even reach the tests.
    //
    // So the whole searchable-options chain is switched off: there is no
    // index to build, and there is nothing to put in the zip either.
    prepareJarSearchableOptions {
        enabled = false
    }
    jarSearchableOptions {
        enabled = false
    }

    test {
        // The unit tests are PLAIN JVM tests: they exercise the MCP protocol
        // client and the payload parsers, neither of which touches an IDE
        // API, and they deliberately use no platform test fixtures.
        //
        // The IntelliJ Platform plugin points the test JVM at the IDE's own
        // class loader (`-Djava.system.class.loader=…PathClassLoader`), which
        // only works together with the `--add-exports` the platform's
        // argument provider supplies — and that provider crashes on this
        // layout ("Index: 1, Size: 1") before a single test runs. These
        // tests want no IDE at all, so both are removed: the provider, and
        // the system property that asks for the IDE class loader.
        jvmArgumentProviders.clear()
        doFirst {
            systemProperties.remove("java.system.class.loader")
            systemProperties.remove("idea.force.use.core.classloader")
            systemProperties.remove("idea.use.core.classloader.for.plugin.path")
        }
        testLogging {
            events("passed", "failed", "skipped")
        }
    }
}
