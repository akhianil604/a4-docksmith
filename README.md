# Docksmith

Run everything from WSL/Ubuntu.

## One-time offline base import

```bash
bash scripts/import-base.sh
```

## Full verification

```bash
bash verify.sh
```

## Faculty demo flow

```bash
go build -o docksmith ./
bash scripts/import-base.sh
rm -f ~/.docksmith/cache/index.json ~/.docksmith/images/myapp/latest.json ~/.docksmith/images/envdemo/latest.json ~/.docksmith/images/writecheck/latest.json
./docksmith build -t myapp:latest ./examples/hello-basic
./docksmith build -t myapp:latest ./examples/hello-basic
./docksmith images
./docksmith run myapp:latest
./docksmith build -t envdemo:latest ./examples/env-override
./docksmith run -e TARGET=faculty envdemo:latest
./docksmith build -t writecheck:latest ./examples/write-check
rm -f /work/docksmith-faculty-leak.txt
./docksmith run -e OUTPUT_FILE=/work/docksmith-faculty-leak.txt writecheck:latest
test ! -e /work/docksmith-faculty-leak.txt && echo PASS
./docksmith rmi myapp:latest
```
