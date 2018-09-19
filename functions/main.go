package functions

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/kms"
	"github.com/aws/aws-sdk-go/service/ssm"
)

var ssmSvc *ssm.SSM
var kmsSvc *kms.KMS
var ssmCache = map[string]string{}
var ssmPathCache = map[string]map[string]string{}
var kmsCache = map[string]string{}

// We do this lazily so we only fail for region/credentials issues when the
// AWS-specific template function are first used.
func bootstrapClients() error {
	var err error
	if ssmSvc == nil || kmsSvc == nil {
		sess, err := newAwsSession()
		if err != nil {
			return err
		}
		ssmSvc = ssm.New(session.Must(sess, err))
		kmsSvc = kms.New(session.Must(sess, err))
	}
	return err
}

func newAwsSession() (*session.Session, error) {
	sess := session.Must(session.NewSession())
	if len(aws.StringValue(sess.Config.Region)) == 0 {
		meta := ec2metadata.New(sess)
		identity, err := meta.GetInstanceIdentityDocument()
		if err != nil {
			m := "AWS_REGION is unset or incorrect and could not determine region via service metadata: %s"
			return nil, fmt.Errorf(m, err)
		}
		return session.NewSession(&aws.Config{
			Region: aws.String(identity.Region),
		})
	}
	return sess, nil
}

func FuncMap() template.FuncMap {
	m := map[string]interface{}{
		"ssm":     ssmValue,
		"ssmJSON": ssmJSON,
		"ssmPath": ssmPath,
		"kms":     kmsValue,
	}

	for k, v := range sprig.TxtFuncMap() {
		m[k] = v
	}

	return m
}

func ssmValue(key string, isEncrypted bool) string {
	if err := bootstrapClients(); err != nil {
		panic(err)
	}

	if val, ok := ssmCache[key]; ok {
		return val
	}

	resp, err := ssmSvc.GetParameter(&ssm.GetParameterInput{
		Name:           &key,
		WithDecryption: aws.Bool(isEncrypted),
	})

	if err != nil {
		panic(err)
	}

	ssmCache[key] = *resp.Parameter.Value
	return ssmCache[key]
}

func ssmJSON(key string, isEncrypted bool) map[string]interface{} {
	if err := bootstrapClients(); err != nil {
		panic(err)
	}

	data := ssmValue(key, isEncrypted)
	m := map[string]interface{}{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		panic(err)
	}
	return m
}

func ssmPath(path string, isEncrypted bool, recursive bool) map[string]string {
	if err := bootstrapClients(); err != nil {
		panic(err)
	}

	if val, ok := ssmPathCache[path]; ok {
		return val
	}

	m := map[string]string{}

	inputParams := &ssm.GetParametersByPathInput{
		Path:           aws.String(path),
		Recursive:      aws.Bool(recursive),
		WithDecryption: aws.Bool(isEncrypted),
	}

	objects := []*ssm.Parameter{}
	err := ssmSvc.GetParametersByPathPages(inputParams,
		func(page *ssm.GetParametersByPathOutput, lastPage bool) bool {
			objects = append(objects, page.Parameters...)
			return true
		})
	if err != nil {
		panic(err)
	}

	for _, v := range objects {
		m[*v.Name] = *v.Value
	}
	ssmPathCache[path] = m
	return m
}

func kmsValue(cipherText string) string {
	if err := bootstrapClients(); err != nil {
		panic(err)
	}

	if val, ok := kmsCache[cipherText]; ok {
		return val
	}

	blob, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		panic(err)
	}

	resp, err := kmsSvc.Decrypt(&kms.DecryptInput{CiphertextBlob: blob})
	if err != nil {
		panic(err)
	}

	kmsCache[cipherText] = string(resp.Plaintext)
	return kmsCache[cipherText]
}
