rm mainapp
go build -gcflags "all=-N -l" -o mainapp
dlv --listen=:2347 --headless=true --api-version=2 exec ./mainapp


