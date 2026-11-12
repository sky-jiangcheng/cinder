#!/bin/bash
# 复现 IDEA 2025.2 "无 stdlib 的 Kotlin 插件" 启动崩溃的最小 harness。
# 用法: bash repro-nostdlib-compile.sh
# 结论写在脚本末尾的 EXPECTED 注释里。

set -euo pipefail

CINDER=/Users/jiangcheng/Workspace/Script/cinder
IDEA_HOME="/Applications/IntelliJ IDEA CE.app/Contents"
PLUG="$HOME/Library/Application Support/JetBrains/IdeaIC2025.2/plugins"
KOTLINC="$IDEA_HOME/plugins/Kotlin/lib/kotlinc.jar"   # 编译器自带 stdlib，不需要外部 stdlib
WORK=$(mktemp -d /tmp/nostdlib-repro.XXXXXX)

echo "workdir: $WORK"
mkdir -p "$WORK/src" "$WORK/out/META-INF"

# 1) 一个最小的 Kotlin 类：任何 Kotlin 构造都会引用 kotlin/jvm/internal/Intrinsics
cat > "$WORK/src/ReproPanel.kt" <<'KT'
class ReproPanel {
    init {
        val parts = listOf("a", "b", "c")          // CollectionsKt
        require(parts.size == 3) { "boom" }         // Intrinsics / PreconditionsKt
        println(parts.joinToString("-") { it.uppercase() })
    }
}
KT

# 2) 用 IDEA 自带的 Kotlin 编译器编（不挂任何 stdlib 到 classpath）
#    产物里只有 ReproPanel.class —— 和发行版 jar 一样：零 stdlib。
find "$IDEA_HOME" -name "kotlin-compiler*.jar" > "$WORK/cp.txt" 2>/dev/null || true
CP=$(paste -sd: "$WORK/cp.txt")
[ -n "$CP" ] || CP="$KOTLINC"

java -cp "$CP" org.jetbrains.kotlin.cli.jvm.K2JVMCompiler \
  -no-stdlib -no-reflect -jvm-target 21 \
  -d "$WORK/out" "$WORK/src/ReproPanel.kt" 2> "$WORK/compile.log" || {
    echo "compile failed:"; tail -20 "$WORK/compile.log"; exit 1; }

echo "== 编译产物（无 stdlib，与 repotory-jetbrains-2.32.x.jar 同构） =="
find "$WORK/out" -name "*.class"

# 3) plugin.xml：id 故意 ≠ 目录名，复现 35396 上传件的形态
#    （installed jar 的目录名是 repotory-jetbrains，而 id 是 dev.repotory.jetbrains）
cat > "$WORK/out/META-INF/plugin.xml" <<'XML'
<idea-plugin>
    <id>dev.repotory.jetbrains</id>
    <name>Cinder Memory</name>
    <vendor>Cinder</vendor>
    <description><![CDATA[repro: kotlin plugin with NO stdlib on its classpath]]></description>
    <depends>com.intellij.modules.platform</depends>
</idea-plugin>
XML

# 4) 打包 —— 目录名 ≠ 插件 id（复现上传件的命名不一致）
PKG="$WORK/repotory-jetbrains"
mkdir -p "$PKG/lib"
(cd "$WORK/out" && zip -qr "$PKG/lib/repotory-jetbrains-9.9.9.jar" .)

# 5) 装进当前运行中的 IDEA（备份原件）
if [ -d "$PLUG/repotory-jetbrains" ]; then
  mv "$PLUG/repotory-jetbrains" "$PLUG/repotory-jetbrains.bak.$(date +%s)"
fi
mv "$PKG" "$PLUG/repotory-jetbrains"
echo "已替换为复现插件。备份原件在 $PLUG/repotory-jetbrains.bak.*"
echo
echo "EXPECTED: 重启 IDEA → 'Cinder Memory' 加载失败"
echo "          Could not initialize class ReproPanel / NoClassDefFoundError: kotlin/jvm/internal/Intrinsics"
echo "还原: rm -rf '$PLUG/repotory-jetbrains' && mv '$PLUG/repotory-jetbrains.bak.<ts>' '$PLUG/repotory-jetbrains'"
