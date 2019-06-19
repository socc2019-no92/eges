import subprocess, json, glob
import time
import os.path
import configparser


config_file_name = 'config.json'
#!! No trailing /
code_path = None
accounts = []
bootstrap_accounts = []

#default values
config = None
machines = []


def prepare():
    for machine in machines:
        print("Killing machine: %s" % machine)
        cmd = ["ssh", "%s" % machine, "pkill geth; pkill -9 geth "]
        print(cmd)
        subprocess.run(cmd)

def parse_config():
    global config
    config = json.load(open(config_file_name))

    global machines 
    for i in range(len(config['cluster'])):
        machines.append(config['cluster'][i]['ip'])
    print(machines)

    global code_path
    code_path = config['code_path']

    


def main():
    parse_config()


    prepare()




if __name__ == "__main__":
    main()
