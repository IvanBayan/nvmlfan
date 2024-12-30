package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	 "time"
	 "sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"gopkg.in/yaml.v3"
)

// GPUConfig holds the configuration for a single GPU card.
type GPUConfig struct {
	Mode   string    `yaml:"mode"`   // Control mode (e.g., "curve" or "target").
	Target int       `yaml:"target"` // Target temperature for PID control.
	PID    []float64 `yaml:"pid"`    // PID control coefficients [Kp, Ki, Kd].
	Curve  [][2]int  `yaml:"curve"`  // Fan curve
}

type Config struct {
	Foreground bool               `yaml:"foreground"`
	Verbosity  int                `yaml:"verbosity"`
	Period     int                `yaml:"period"`
	Cards      map[int]GPUConfig  `yaml:"cards"`
	Logging    map[string]string `yaml:"logging"`
}

const (
	defaultPeriod = 1
	defaultLoggingType = "stdout"
	defaultLoggingLevel = "info"
)
var config Config

func isFlagPassed(name string) bool {
    found := false
    flag.Visit(func(f *flag.Flag) {
        if f.Name == name {
            found = true
        }
    })
    return found
}

func loadConfig(path string) Config {
	var cfg Config

	// Open the configuration file
	file, err := os.Open(path)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer file.Close()

	// Decode the YAML configuration
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		log.Fatalf("%v", err)
	}
	return cfg
}

func ConfigureLogging() {
	var logType, logLevel string
	if config.Logging == nil {
		slog.Warn("No logging configuration provided, using default settings.")
		logType = defaultLoggingType
		logLevel = defaultLoggingLevel		
	} else {
		logType = config.Logging["type"]
		logLevel = config.Logging["level"]
	}

	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		slog.Warn("Invalid log level, defaulting to 'info'.", "logLevel", logLevel)
		level = slog.LevelInfo
	}
	// Set up log handler
	var handler slog.Handler
	switch logType {
	case "stdout":
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	case "file":
		filePath := config.Logging["path"]
		if filePath == "" {
			filePath = "/var/log/nvmlfan.log" // Default log file
		}
		file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("Failed to open log file '%s': %v", filePath, err)
		}
		handler = slog.NewTextHandler(file, &slog.HandlerOptions{Level: level})
	default:
		slog.Warn("Invalid log type, defaulting to 'stdout'.", "logType", logType)
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}

	slog.SetDefault(slog.New(handler))
	slog.Debug("Global logging configured successfully.")
}

func ListGPUs() {
	deviceCount := GetDeviceCount()
	for idx := 0; idx < deviceCount; idx++ {
		PrintCardInfo(idx)
	}
	nvml.Shutdown()
	os.Exit(0)
}

func PrintCardInfo(idx int) {
	device := DeviceGetHandleByIndex(idx)
	sn, ret := device.GetSerial()
	if ret != nvml.SUCCESS {
		log.Fatalf("Can't get serial number",  "GPU", idx, "error",  nvml.ErrorString(ret))
	}
	uuid, ret := device.GetUUID()
	if ret != nvml.SUCCESS {
		log.Fatalf("Can't get UUID",  "GPU", idx, "error",  nvml.ErrorString(ret))
	}
	name, ret := device.GetName()
	if ret != nvml.SUCCESS {
		log.Fatalf("Can't get name",  "GPU", idx, "error",  nvml.ErrorString(ret))
	}
	minSpeed, maxSpeed, maxTemp := GetThermalInfo(idx)
	temp := GetTemperature(idx)
	fmt.Printf("%2d: %v (s/n: %v) - %v\n", idx, name, sn, uuid)
	fmt.Printf("  +- Temp: %d Max temp: %d\n", temp, maxTemp)
	for i := 0; i<GetNumFans( idx ); i++ {
		policy, ret := device.GetFanControlPolicy_v2(i)
		if ret != nvml.SUCCESS {
			slog.Error("Can't get fan control policy", "GPU", idx, "fan", i, "error", ret)
			os.Exit(1)
		}
		speed, ret := device.GetFanSpeed_v2(i)
		if ret != nvml.SUCCESS {
			slog.Error("Can't get fan speed", "GPU", idx, "fan", i, "error", ret)
			os.Exit(1)
		}
		fmt.Printf("  +- Fan: %d Speed: %d Range: %d-%d Policy: %+v\n", i, speed, minSpeed, maxSpeed, policy)
	}

}

func GetDeviceCount() int {
	deviceCount, err := nvml.DeviceGetCount()
	if err != nvml.SUCCESS {
		slog.Error("Can't get device count", "error", err)
	}
	return deviceCount
}

func DeviceGetHandleByIndex(idx int) nvml.Device {
	device, ret := nvml.DeviceGetHandleByIndex(idx)
	if ret != nvml.SUCCESS {
		log.Fatalf("Error getting handle for GPU %d: %v", idx, ret)		
	}
	return device
}

func DefaultFansSpeed(idx int) {
	device := DeviceGetHandleByIndex(idx)
	fan_count := GetNumFans(idx)	
	for fan_index := 0; fan_index < fan_count; fan_index++ {
		err := device.SetDefaultFanSpeed_v2(fan_index);
		if err != nvml.SUCCESS {
			slog.Error("Error resetting fan speed", "fan", fan_index, "error", err)
		}
		slog.Debug("Default fan control restored", "fan", fan_index)
	}
}

func Shutdown(ret int) {
	var once sync.Once
	once.Do(func() {
		slog.Info("Restoring default fan controls")
		deviceCount := GetDeviceCount()

		for i := 0; i < deviceCount; i++ {
			slog.Info("Setting fans to default mode", "GPU", i)
			DefaultFansSpeed(i)
		}
		nvml.Shutdown()
		os.Exit(ret)
	})
}

func GetNumFans( idx int) int {
	device := DeviceGetHandleByIndex(idx)
	fan_count, ret := device.GetNumFans()
	if ret != nvml.SUCCESS {
		slog.Error("Unable to get fan count of device", "error", nvml.ErrorString(ret))
	}
	return fan_count
}

func GetMinMaxFanSpeed(device nvml.Device) (int, int) {
	minSpeed, maxSpeed, ret := device.GetMinMaxFanSpeed()
	if ret != nvml.SUCCESS {
		slog.Error("Error can't get min/max fan speed", "error", ret)		
	}
	return minSpeed, maxSpeed
}

func GetMaxGPUTempThreshold(device nvml.Device) int {
	temp, ret := device.GetTemperatureThreshold( nvml.TEMPERATURE_THRESHOLD_GPU_MAX)
	if ret != nvml.SUCCESS {
		slog.Error("Error can't get max temperature threshold", "error", ret)		
	}
	return int(temp)
}

func GetTemperature(idx int) int {
	device := DeviceGetHandleByIndex( idx )
	temp, err := device.GetTemperature(nvml.TEMPERATURE_GPU)
	if err != nvml.SUCCESS {
		slog.Error("Can't get temperature", "GPU", idx, "error", err)
	}
	return int(temp)
}

// ComputeFanSpeed calculates the fan speed based on the temperature and the curve.
func ComputeFanSpeed(temp int, curve [][2]int, minSpeed, maxSpeed int) int {
	// If temperature is below the first point in the curve
	if temp < curve[0][0] {
		return minSpeed
	}

	// If temperature is above the last point in the curve
	if temp > curve[len(curve)-1][0] {
		return maxSpeed
	}

	// If temperature is between two points in the curve
	for i := 0; i < len(curve)-1; i++ {
		t1, f1 := curve[i][0], curve[i][1]
		t2, f2 := curve[i+1][0], curve[i+1][1]

		if temp >= t1 && temp <= t2 {
			// Linear interpolation
			return f1 + (f2-f1)*(temp-t1)/(t2-t1)
		}
	}

	// Default return value (should not reach here)
	return maxSpeed
}

func SetFanSpeed( idx int, speed int ) {
	device := DeviceGetHandleByIndex( idx )
	fanCount, ret := device.GetNumFans()
	if ret != nvml.SUCCESS {
		slog.Error("Unable to get fan count of device", "GPU", idx, "error", nvml.ErrorString(ret))
	}
	for fi := 0; fi < fanCount; fi++ {
		target_speed, ret:= device.GetTargetFanSpeed(fi)
		if( target_speed == speed) {
			slog.Debug("Skip, speed unchanged", "GPU", idx, "fan", fi)
			continue
		}
		ret = device.SetFanSpeed_v2(fi, speed)
		if ret != nvml.SUCCESS {
			log.Fatalf("Unable to set fan %d speed %d: %v\n", fi, speed, nvml.ErrorString(ret))
				Shutdown(1)
		}
	}
}

func GetThermalInfo(idx int ) (int, int,int) {
	device := DeviceGetHandleByIndex( idx )
	minSpeed, maxSpeed := GetMinMaxFanSpeed(device)
	slog.Debug("Fan speed range", "GPU", idx, "min", minSpeed, "max", maxSpeed)
	maxTemp := GetMaxGPUTempThreshold(device)
	slog.Debug("Max temperature", "GPU", idx, "temp", maxTemp)
	return minSpeed, maxSpeed, maxTemp
}

func FanCurveControl( idx int ) {
	slog.Info("Curve control", "GPU", idx)
	minSpeed, maxSpeed, maxTemp := GetThermalInfo(idx)	
	curve := config.Cards[idx].Curve

	// Clamp curve
	slog.Debug("Clamping curve", "dump", curve)
	for i, point := range curve {
		if point[0] > maxTemp {
			slog.Debug("Clamping temperature above maximum GPU threshold", "GPU", idx, "temp", point[0], "point", i, "max", maxTemp)
			point[0] = maxTemp
		}
		if point[1] < minSpeed {
			slog.Debug("Clamping fan below allowed range", "GPU", idx, "speed", point[0], "point", i, "min", minSpeed)
			point[1] = minSpeed
		}
		if point[1] > maxSpeed {
			slog.Debug("Clamping fan above allowed range", "GPU", idx, "speed", point[0], "point", i, "max", maxSpeed)
			point[1] = maxSpeed
		}
		if i > 0 {
			if point[0] <= curve[i-1][0] {
				slog.Error("Temperature curve is not increasing", "GPU", idx, "point", i-1, "next", i)
			}
			if point[1] <= curve[i-1][1] {
				slog.Error("Fan speed curve is not increasing", "GPU", idx, "point", i-1, "next", i)
			}
		}
		curve[i] = point
	}
	slog.Debug("Clamped curve", "dump", curve)
	slog.Debug("Starting control loop", "GPU", idx)
	for {
		temp := GetTemperature(idx)
		speed := ComputeFanSpeed(temp, curve, minSpeed, maxSpeed)
		slog.Debug("Setting new speed", "GPU", idx, "speed", speed, "temp", temp)
		SetFanSpeed(idx, speed)
		time.Sleep(time.Duration(config.Period) * time.Second)
	}
}



func FanTargetControl( idx int ) {
	slog.Info("Target control", "GPU", idx)
	iminSpeed, imaxSpeed, _ := GetThermalInfo(idx)	

	minSpeed := float64(iminSpeed)
	maxSpeed := float64(imaxSpeed)
	gpu_config := config.Cards[idx]
	target := gpu_config.Target
	kp := gpu_config.PID[0]
	ki := gpu_config.PID[1]
	kd := gpu_config.PID[2]
	var pid_error, pid_prevError, iacc float64;

	for {
		temp := GetTemperature(idx)
		// Invert direction of pid
		pid_error = - float64(target - temp)
		pTerm := pid_error * kp
		dError := pid_error - pid_prevError
		dTerm := kd * dError
		iTerm := ki * pid_error		
		pid_prevError = pid_error

		// Antiwindup
		// If proportional and integral part out of range
		// and integral is changing in the same direction
		// integral accumulator is winding up
		if pTerm + iacc > maxSpeed && iTerm > 0 ||
		   pTerm + iacc < minSpeed && iTerm < 0 {
			slog.Debug("PID antiwindup triggered", "iTerm", iTerm)
			iTerm = 0
		}
		iacc += iTerm
		
		output := int(pTerm + iacc + dTerm)

		// Clamp output
		if output < iminSpeed {
			slog.Debug("PID clamping output to min", "output", output, "min", iminSpeed)
			output = iminSpeed
		} else if output > imaxSpeed {
			slog.Debug("PID clamping output to max", "max", output, "max", imaxSpeed)
			output = imaxSpeed
		}
		
		slog.Debug("PID state", "kp", kp, "ki", ki, "kd", kd,
                  "dError", dError, "pTerm", pTerm, "iacc", iacc, "dTerm", dTerm,
				  "input", temp, "output", output, "pid_error", pid_error)
		SetFanSpeed(idx, output)
		time.Sleep(time.Duration(config.Period) * time.Second)
	}

}

func ControlFans() {
	slog.Debug("Cards configurations", "dump", config.Cards)
	deviceCount := GetDeviceCount()
	for idx := 0; idx < deviceCount; idx++ {
		gpu_config, ok := config.Cards[idx]
		if  ! ok {
			slog.Info("Skipping card, not found in config.", "GPU", idx)
			continue
		} else {
			slog.Info("Taking FAN controls of card.", "GPU", idx)
		}
		if gpu_config.Mode == "curve" {
			go FanCurveControl(idx)
		} else if gpu_config.Mode == "target" {
			go FanTargetControl(idx)
		} else {
			slog.Error("Wrong card mode", "GPU", idx, "mode", gpu_config.Mode)
		}
	}
}

func main() {
	// Command-line arguments
	foreground := flag.Bool("foreground", false, "Run in foreground")
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	list := flag.Bool("list", false, "List GPUs")
	restore := flag.Bool("restore", false, "Restore fan controll on all GPUs")
	flag.Parse()
	
	if err := nvml.Init(); err != nvml.SUCCESS {
		slog.Error("Failed to initialize NVML", "error", err)
		os.Exit(1)
	}

	if *list {
		ListGPUs()
	}

	if *restore {
		Shutdown(0)
	}
	defer Shutdown(0)

	// Load configuration
	config = loadConfig(*configPath)
	ConfigureLogging()
	slog.Debug("Config successfully loaded", "dump", config)

	if config.Period == 0 {
		config.Period = defaultPeriod
	}

	// Conditionally override configuration only if the flags are passed by the user
	if isFlagPassed("foreground") {
		config.Foreground = *foreground
		slog.Debug("Using command line flag for foreground")
	} 

	if !config.Foreground {
		slog.Debug("Daemonizing")
		if err := daemonize(); err != nil {
			slog.Error("Failed to daemonize", "error", err)
			Shutdown(1)
		}
	}

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("Starting fan control")
	ControlFans()

	<-stop
	slog.Info("Shutting down fan control")
}

func daemonize() error {
	// Fork process to run as a daemon
	if os.Getppid() != 1 {
		attr := &os.ProcAttr{
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		}
		proc, err := os.StartProcess(os.Args[0], os.Args, attr)
		if err != nil {
			return err
		}
		proc.Release()
		Shutdown(0)
	}
	return nil
}