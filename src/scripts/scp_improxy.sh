#!/usr/bin/env bash
if [ "$#" -ne 1 ]; then
    echo "Please input hostname"
    exit -1
fi

host_name=$1

# 更新配置
scp -r conf root@${host_name}:/usr/local/service/improxy/


# 更新improxy
ssh root@${host_name} "rm -rf /usr/local/service/improxy/improxy /usr/local/video/improxy_bk"
scp improxy root@${host_name}:/usr/local/video/improxy/

# 创建工作目录
ssh root@${host_name} "mkdir -p /data/tmp_improxy/cache"
# /data/tmp_improxy/cache 目录太大了，直接修改chown比较慢
ssh root@${host_name} "chown worker.worker /data/tmp_improxy"
ssh root@${host_name} "chown worker.worker /data/tmp_improxy/cache"


# 启动服务
# 拷贝systemctl
# scp scripts/improxy.service root@${host_name}:/lib/systemd/system/improxy.service
# ssh root@${host_name} "systemctl daemon-reload"
# ssh root@${host_name} "systemctl restart improxy"

