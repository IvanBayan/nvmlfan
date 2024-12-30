# nvmlfan
Thinkfan like tool to control nvidia GPU's fans.  
That tool doesn't require X server and has two modes to control temperature "curve" and "target".

# Discalimer
This tool is provided "as is" without any warranties, expressed or implied. The author assumes no responsibility for any damage, malfunction, or performance issues that may arise from its use. Adjusting GPU fan settings may void warranties or cause hardware damage if improperly configured. Use this tool at your own risk. Always ensure proper cooling and monitor hardware temperatures during operation.

# Limitations
Demon controls only GPU temperature (ignores other temperatures like memory).
Demon controls all fans at once, even if there is more then one fan, they will be set to the same "speed".

# Modes
## mode: curve
```yaml
cards:
  0:
    mode: curve
    curve:
    # - [ temperature, fan_speed ]
      - [ 60, 30 ]
      - [ 65, 50] 
      - [ 75, 100]
```
Basicaly *curve* mode maps temperature to fan "speed".  
'curve' array defines anchor points, between which temperature to fan speed interpolated.  
Minimum fan speed limited by GPU, all fan speeds below that limit enforced to GPU limit.  
Maximum temperature caped by GPU, all speeds above that limit enforced to GPU limit.
If last point below maximum GPU threshold, fan speed will be approximated from last point to 100% on maximum thershold temperature.  

## mode: target
```yaml
cards:
  0:
    mode: target
    target: 65
    pid: [ 20, 0.1, 0 ]
```
This mode tries to maintain constant temperature of GPU, which involves usage of PID controller. To make it work PID coefficients shuld be tuned and here is no simple answer how to do it. However once tuned, they should be the same for the same models of GPU. 

In the [ 20, 0.1, 0 ] array:
* The first number (P) is the proportional parameter. It controls how much the fan speed changes when the error (the difference between the target temperature and the actual temperature) equals one. For example, with a target temperature of 65°C and an actual temperature of 70°C, the fan speed would be set to 100%. However, setting the fan to 100% counters the heat, causing the temperature to decrease. This, in turn, reduces the fan speed, which can lead to the temperature rising again, and so on. If the P parameter is too high, this cycle can cause the system to oscillate.
* The second number (I) is the integral parameter. Since the proportional component is quite rough, the integral component adjusts slowly over time to ensure the fan speed perfectly matches the load, maintaining the target temperature.
* The third number (D) is the derivative parameter. It reacts to the rate at which the temperature changes. For systems with significant inertia, such as this one, the derivative component can often be omitted. If you’re curious about how to use it effectively, you’ll need to dive into some control theory books.

It's a good starting point for PID tuning: https://en.wikipedia.org/wiki/Proportional%E2%80%93integral%E2%80%93derivative_controller#Manual_tuning

# Dependencies
Nvidia proprietary drivers should be installed. nvidia-smi should detect and show cards. libnvidia-ml should be installed (for debian `apt install libnvidia-ml`).

# Build
```console
$ git clone git@github.com:IvanBayan/nvmlfan.git
$ cd nvmlfan
$ go build nvmlfan.go
```

# Installation
```console
# install -o root -g root -m 755 <repo_path>/nvmlfan /usr/local/sbin/nvmlfan
# install -o root -g root -m 644 <repo_path>/nvmlfan.service /etc/systemd/system/nvmlfan.service
# install -o root -g root -m 644  <repo_path>/config-example.yaml /usr/local/etc/nvmlfan.yaml
# vi /usr/local/etc/nvmlfan.yaml
```

# Configuration
