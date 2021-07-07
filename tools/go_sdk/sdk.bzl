def _gen_imports_impl(ctx):
    ctx.file("BUILD", "")

    is_nixos = "NIX_PATH" in ctx.os.environ
    bzl_file_content = """
load(
    "@io_bazel_rules_go//go:deps.bzl",
    "go_register_toolchains",
    "go_rules_dependencies",
)
def load_go_sdk():
    go_rules_dependencies()
    go_register_toolchains({go_version})
    """.format(
        go_version = 'go_version = "host"' if is_nixos else "",
    )

    ctx.file("imports.bzl", bzl_file_content)

_gen_imports = repository_rule(
    implementation = _gen_imports_impl,
    attrs = dict(),
)

def gen_imports(name):
    _gen_imports(
        name = name,
    )
