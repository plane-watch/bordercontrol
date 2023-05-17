
# check beast connection inbound
ss -ntH state established | tr -s " " | cut -d " " -f 3 | grep ":12345"
