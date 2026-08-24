.[]
| select(.created_at < $cutoff)
| select((.metadata.container.tags // []) | length > 0)
| select(all(.metadata.container.tags[];
    test("^pr-[0-9]+-sha256-[0-9a-f]{64}$")))
| [.id, .created_at, (.metadata.container.tags | join(","))]
| @tsv
