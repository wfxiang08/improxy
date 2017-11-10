package config

import (
	"github.com/wfxiang08/cyutils/utils"
	cy_config "github.com/wfxiang08/cyutils/utils/config"
	"log"
	"os"
	"path"
)

var (
	AwsAccessKeyId     string
	AwsSecretAccessKey string
	AWSBuckets         string
	AwsRegion          string
	SimpleKey          []byte
	MagicNum           int64
)

func init() {
	config := cy_config.NewCfg(GetConfPath("conf/aws.ini"))
	config.Load()
	AwsAccessKeyId, _ = config.ReadString("aws_access_key_id", "")
	AwsSecretAccessKey, _ = config.ReadString("aws_secret_access_key", "")
	AWSBuckets, _ = config.ReadString("aws_buckets", "")
	AwsRegion, _ = config.ReadString("aws_region", "")

	simpleKey, _ := config.ReadString("simple_key", "")
	SimpleKey = []byte(simpleKey)

	magicNum, _ := config.ReadInt("magic_num", 0)
	MagicNum = int64(magicNum)

}

// 通过相关路径获取项目的资源时，在testcase和运行binary时的表现不太一样，各自的pwd有点点差别
func GetConfPath(filePath string) string {
	pwd, _ := os.Getwd()
	i := 0
	for !utils.FileExist(path.Join(pwd, filePath)) {
		pwd = path.Dir(pwd)
		i++
		// 找不到配置文件，就报错
		if i >= 3 {
			log.Panicf("ConfigPath not found: %s", filePath)
		}
	}
	return path.Join(pwd, filePath)

}
