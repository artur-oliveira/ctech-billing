module gopkg.aoctech.app/billing/api

// The patch is part of the requirement, not decoration. `go 1.26` is a floor,
// so CI's `go-version-file: api/go.mod` resolved it to whatever patch the runner
// happened to offer — 1.27 — while a developer on 1.27 saw a clean
// govulncheck and CI saw five standard-library findings, every one of them
// "Fixed in: go1.27". A version file that does not pin the patch is a version
// file that does not make the two machines agree.
//
// Same pin as ctech-account and ctech-poker, which are the sibling repositories
// that also run govulncheck. When the next standard-library advisory lands, the
// scanner goes red and the fix is this line.
go 1.27.2

require (
	github.com/aws/aws-sdk-go-v2 v1.47.1
	github.com/aws/aws-sdk-go-v2/config v1.33.5
	github.com/aws/aws-sdk-go-v2/credentials v1.20.5
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.21.5
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.69.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.109.1
	github.com/aws/smithy-go v1.28.1
	github.com/caarlos0/env/v11 v11.4.1
	github.com/carlos7ags/folio v0.10.1
	github.com/gofiber/fiber/v3 v3.5.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/oklog/ulid/v2 v2.1.2
	github.com/valkey-io/valkey-go v1.0.78
	gopkg.aoctech.app/api-commons v1.14.0
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.41.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sesv2 v1.73.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.47.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.0 // indirect
	github.com/gofiber/schema v1.8.7 // indirect
	github.com/gofiber/utils/v2 v2.5.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/molecule-man/go-brrr v1.1.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.74.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
