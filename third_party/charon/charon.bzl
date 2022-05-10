def charon_repo():
    _maybe(
        http_archive,
        name = "charon",
        urls = [
            "https://github.com/ObolNetwork/charon/archive/d5e6741957ca91d1ca55a58772e072ef5b466abe.tar.gz",
        ],
        build_file = "@prysm//third_party/charon:charon.BUILD",
    )
