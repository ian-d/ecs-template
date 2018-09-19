# ecs-template

AWS's [Elastic Container Service](https://aws.amazon.com/ecs/) has a secrets problem: AWS provides services like [SSM/Parameter Store](https://docs.aws.amazon.com/systems-manager/latest/userguide/systems-manager-paramstore.html) and [KMS](https://aws.amazon.com/kms/) for secret management but they are not integrated into ECS task definitions. This is left up to the user, leading to unnecessary container dependencies for custom entrypoints (bigger images, more per-application code) or SSM/KMS aware containerized applications (not very 12 Factor-y).

`ecs-template` provides a single, small binary that provides helpful ENV/SSM/KMS functions for [Go template](https://golang.org/pkg/text/template/) files stored locally or fetched from external sources (S3, Git, HTTP). Once `ecs-template` has moved the source files, directories, and archives (described below) to their destinations on the local filesystem it then parses the destination files in-place as if they were a [Go template](https://golang.org/pkg/text/template/).

As part of an entrypoint shell script for Docker containers running in ECS it provides a simple way to have external per-environment configuration bundles in S3 and keep applications SSM/KMS agnostic by externalizing secret fetching.

## Basic Usage
```
$ aws ssm put-parameter \
  --name 'my_app_username' \
  --type "SecureString" \
  --value 'scott'

$ aws ssm put-parameter \
  --name 'my_app_password' \
  --type "SecureString" \
  --value 'tiger'

$ cat example.tmpl
username={{ ssm "my_app_username" true }}
password={{ ssm "my_app_password" true }}

$ ecs-template -f "example.tmpl, example.out"
2018/09/19 15:34:11 Fetching file example.tmpl to destination example.out
2018/09/19 15:34:11 Template parsing file: example.out

$ cat example.out
username=scott
password=tiger
```

Or using a local file to export secrets as ENV vars:
```
$ cat env.tmpl
export MY_APP_USERNAME={{ ssm "my_app_username" true }}
export MY_APP_PASSWORD={{ ssm "my_app_password" true }}

$ ecs-template -f "env.tmpl, env.out" \
  && source env.out \
  && rm env.out
2018/09/19 15:50:40 Fetching file env.tmpl to destination env.out
2018/09/19 15:50:40 Template parsing file: env.out

$ env | grep MY_APP
MY_APP_USERNAME=scott
MY_APP_PASSWORD=tiger
```

## Template Functions
`ecs-template` provides template functions to fetch secrets from AWS services. All of the template functions from [sprig](http://masterminds.github.io/sprig/) are also included.

### `ssm <key> <isEncryted>`
Returns the value of the SSM `<key>` and requires a `true|false` value for `<isEncrypted>` (no default value):
```
username={{ ssm "my_app_username" false }}
password={{ ssm "my_app_password" true }}
```

### `ssmPath <path> <isEncrypted> <recursive>`
Returns a list of fully path-qualified keys and values. Assume the paths `/app/prod/db/password` and `/app/prod/db/username` exist and `ENV` environment variable is `prod`:
```
{{ $base := (print "/app/" (env "ENV")) -}}
{{ $creds := (ssmPath $base true true) -}}
username={{ index $creds (print $base "/db/username") }}
password={{ index $creds (print $base "/db/password") }}
```

### `ssmJSON <key> <isEncrypted>`
Assumes the value of `<key>` is JSON, parses it, and returns a map. Assume the value for SSM key `my_app` is `{ username: "foo", password: "bar" }`:
```
username={{ (ssmJSON "my_app" false).username }}
password={{ (ssmJSON "my_app" false).password }}
```

### `kms <base64blob>`
Assumes `base64blob` is a Base64-encoded KMS ciphertext blob:

```
username={{ kms (env "APP_USER_ENC") }}
password={{ kms (env "APP_PASSWORD_ENC") }}
```

## Sources & External Locations
All source locations may be either local files/directories or any of the external URL formats supported by go-getter: [go-getter URL formats](https://github.com/hashicorp/go-getter#url-format), which includes git, http, s3.

Before being fetched/moved, all source arguments are first parsed as templates, allowing sources to be dynamically defined:
```
$ ecs-template \
  --dir "my-bucket.bucket.s3.amazonaws.com/{{env "ENV"}}-config.tar.gz, /home/app/config"
  --file ""
```

### Directories & Archives (`--dir <source, dest>`)
Archives are automatically unpacked into the `<dest>` directory, which will be created if it does not exist. Supported archive formats are those included by default by [go-getter](https://github.com/hashicorp/go-getter#unarchiving).

### Globs (`--glob <pattern>`)
Globs are considered to be in-place file templates, but are not evaluated until after directories and archives have been moved and unpacked. (Except in manifests, see below.) Globs can therefore refer to files that only exist once an archive/dir has been moved:
```
$ ecs-template \
  --dir "my-bucket.bucket.s3.amazonaws.com/{{env "ENV"}}-config.tar.gz, /home/app/config"
  --glob "/home/app/config/**/*.conf"
```
Recursive matches with `**` are supported, but `~` for home directories is not. Be sure to quote globs flags so they don't go through shell expansion.

### Files (`--file <source>, <dest>` or `--file <dest>`)
Files are either `<source>,<dest>` pairs where the source location is copied to the destination location (full path + filename) before the destination location is parsed as a template. Using just `<dest>` means the file will be parsed as a template *in place*. Using just `<dest>` will **overwrite** the file with the results of the template parsing, ***so be very careful***. It is intended to be used inside a container.

### Manifests (`--manifest <source>`)
Manifests are YAML files that list directories, globs, and files to parse. This is useful for per-env or generic container reuse (eg, a generic Tomcat image that pulls different configurations/secrets at launch):

```
ecs-template \
  --manifest "my-bucket.bucket.s3.amazonaws.com/{{env "ENV"}}.yaml"
```

An example manifest YAML:
```
dirs:
  - "my-bucket.bucket.s3.amazonaws.com/{{env "ENV"}}-config.tar.gz, /home/app/config"
globs:
  - "/home/app/config/**/*.conf"
  - "/home/app/config/**/*.yaml"
files:
  - "/home/app/config/app.env"
```

Multiple `--manifest` flags may be used, but directories and globs are evaluated at manifest parse time, per manifest.
