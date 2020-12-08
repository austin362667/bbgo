package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"text/template"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	tb "gopkg.in/tucnak/telebot.v2"

	"github.com/c9s/bbgo/pkg/bbgo"
	"github.com/c9s/bbgo/pkg/cmd/cmdutil"
	"github.com/c9s/bbgo/pkg/notifier/slacknotifier"
	"github.com/c9s/bbgo/pkg/notifier/telegramnotifier"
	"github.com/c9s/bbgo/pkg/slack/slacklog"
	"github.com/c9s/bbgo/pkg/types"
)

func init() {
	RunCmd.Flags().Bool("no-compile", false, "do not compile wrapper binary")
	RunCmd.Flags().String("os", runtime.GOOS, "GOOS")
	RunCmd.Flags().String("arch", runtime.GOARCH, "GOARCH")

	RunCmd.Flags().String("config", "", "config file")
	RunCmd.Flags().String("since", "", "pnl since time")
	RootCmd.AddCommand(RunCmd)
}

var wrapperTemplate = template.Must(template.New("main").Parse(`package main
// DO NOT MODIFY THIS FILE. THIS FILE IS GENERATED FOR IMPORTING STRATEGIES
import (
	"github.com/c9s/bbgo/pkg/cmd"

{{- range .Imports }}
	_ "{{ . }}"
{{- end }}
)

func main() {
	cmd.Execute()
}

`))

func compileRunFile(filepath string, config *bbgo.Config) error {
	var buf = bytes.NewBuffer(nil)
	if err := wrapperTemplate.Execute(buf, config); err != nil {
		return err
	}

	return ioutil.WriteFile(filepath, buf.Bytes(), 0644)
}

func runConfig(basectx context.Context, userConfig *bbgo.Config) error {
	ctx, cancelTrading := context.WithCancel(basectx)

	environ := bbgo.NewEnvironment()

	if viper.IsSet("mysql-url") {
		db, err := cmdutil.ConnectMySQL()
		if err != nil {
			return err
		}
		environ.SyncTrades(db)
	}

	if len(userConfig.Sessions) == 0 {
		for _, n := range bbgo.SupportedExchanges {
			if viper.IsSet(string(n) + "-api-key") {
				exchange, err := cmdutil.NewExchangeWithEnvVarPrefix(n, "")
				if err != nil {
					panic(err)
				}
				environ.AddExchange(n.String(), exchange)
			}
		}
	} else {
		for sessionName, sessionConfig := range userConfig.Sessions {
			exchangeName, err := types.ValidExchangeName(sessionConfig.ExchangeName)
			if err != nil {
				return err
			}

			exchange, err := cmdutil.NewExchangeWithEnvVarPrefix(exchangeName, sessionConfig.EnvVarPrefix)
			if err != nil {
				return err
			}

			environ.AddExchange(sessionName, exchange)
		}
	}

	if userConfig.Persistence != nil {
		if err := environ.ConfigurePersistence(userConfig.Persistence); err != nil {
			return err
		}
	}

	notification := bbgo.Notifiability{
		SymbolChannelRouter:  bbgo.NewPatternChannelRouter(nil),
		SessionChannelRouter: bbgo.NewPatternChannelRouter(nil),
		ObjectChannelRouter:  bbgo.NewObjectChannelRouter(),
	}

	// for slack
	slackToken := viper.GetString("slack-token")
	if len(slackToken) > 0 && userConfig.Notifications != nil {
		if conf := userConfig.Notifications.Slack; conf != nil {
			if conf.ErrorChannel != "" {
				log.Infof("found slack configured, setting up log hook...")
				log.AddHook(slacklog.NewLogHook(slackToken, conf.ErrorChannel))
			}

			log.Infof("adding slack notifier with default channel: %s", conf.DefaultChannel)
			var notifier = slacknotifier.New(slackToken, conf.DefaultChannel)
			notification.AddNotifier(notifier)
		}
	}

	// for telegram
	telegramBotToken := viper.GetString("telegram-bot-token")
	telegramAuthToken := viper.GetString("telegram-auth-token")
	if len(telegramBotToken) > 0 && len(telegramAuthToken) > 0 {
		log.Infof("setting up telegram notifier...")

		bot, err := tb.NewBot(tb.Settings{
			// You can also set custom API URL.
			// If field is empty it equals to "https://api.telegram.org".
			// URL: "http://195.129.111.17:8012",
			Token:  telegramBotToken,
			Poller: &tb.LongPoller{Timeout: 10 * time.Second},
		})

		if err != nil {
			return err
		}

		var options []telegramnotifier.NotifyOption

		if environ.PersistenceServiceFacade != nil && environ.PersistenceServiceFacade.Redis != nil {
			options = append(options, telegramnotifier.WithRedisPersistence(environ.PersistenceServiceFacade.Redis))
		}

		log.Infof("send the following command to the bbgo bot you created to enable the notification...")
		log.Infof("===========================================")
		log.Infof("")
		log.Infof("    /auth %s", telegramAuthToken)
		log.Infof("")
		log.Infof("===========================================")
		var notifier = telegramnotifier.New(bot, telegramAuthToken, options...)
		notification.AddNotifier(notifier)
	}

	environ.Notifiability = notification

	if userConfig.Notifications != nil {
		if err := environ.ConfigureNotification(userConfig.Notifications); err != nil {
			return err
		}
	}


	trader := bbgo.NewTrader(environ)

	if userConfig.RiskControls != nil {
		trader.SetRiskControls(userConfig.RiskControls)
	}

	for _, entry := range userConfig.ExchangeStrategies {
		for _, mount := range entry.Mounts {
			log.Infof("attaching strategy %T on %s...", entry.Strategy, mount)
			trader.AttachStrategyOn(mount, entry.Strategy)
		}
	}

	for _, strategy := range userConfig.CrossExchangeStrategies {
		log.Infof("attaching cross exchange strategy %T", strategy)
		trader.AttachCrossExchangeStrategy(strategy)
	}

	for _, report := range userConfig.PnLReporters {
		if len(report.AverageCostBySymbols) > 0 {

			log.Infof("setting up average cost pnl reporter on symbols: %v", report.AverageCostBySymbols)
			trader.ReportPnL().
				AverageCostBySymbols(report.AverageCostBySymbols...).
				Of(report.Of...).
				When(report.When...)

		} else {
			return fmt.Errorf("unsupported PnL reporter: %+v", report)
		}
	}

	if err := trader.Run(ctx); err != nil {
		return err
	}

	cmdutil.WaitForSignal(ctx, syscall.SIGINT, syscall.SIGTERM)

	cancelTrading()

	shutdownCtx, cancelShutdown := context.WithDeadline(ctx, time.Now().Add(30*time.Second))

	log.Infof("shutting down...")
	trader.Graceful.Shutdown(shutdownCtx)
	cancelShutdown()
	return nil
}

var RunCmd = &cobra.Command{
	Use:   "run",
	Short: "run strategies from config file",

	// SilenceUsage is an option to silence usage when an error occurs.
	SilenceUsage: true,

	RunE: func(cmd *cobra.Command, args []string) error {
		configFile, err := cmd.Flags().GetString("config")
		if err != nil {
			return err
		}

		if len(configFile) == 0 {
			return errors.New("--config option is required")
		}

		noCompile, err := cmd.Flags().GetBool("no-compile")
		if err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		userConfig, err := bbgo.LoadBuildConfig(configFile)
		if err != nil {
			return err
		}

		// if there is no custom imports, we don't have to compile
		if noCompile {
			userConfig, err = bbgo.Load(configFile)
			if err != nil {
				return err
			}

			log.Infof("running config without wrapper binary...")
			if err := runConfig(ctx, userConfig); err != nil {
				return err
			}

			return nil
		}

		shouldCompile := len(userConfig.Imports) > 0
		if shouldCompile {
			log.Infof("found imports %v, compiling wrapper binary...", userConfig.Imports)
		}

		var runArgs = []string{"run", "--no-compile"}
		cmd.Flags().Visit(func(flag *flag.Flag) {
			runArgs = append(runArgs, "--"+flag.Name, flag.Value.String())
		})
		runArgs = append(runArgs, args...)

		goOS, err := cmd.Flags().GetString("os")
		if err != nil {
			return err
		}

		goArch, err := cmd.Flags().GetString("arch")
		if err != nil {
			return err
		}

		runCmd, err := buildAndRun(ctx, userConfig, goOS, goArch, runArgs...)
		if err != nil {
			return err
		}

		if sig := cmdutil.WaitForSignal(ctx, syscall.SIGTERM, syscall.SIGINT); sig != nil {
			log.Infof("sending signal to the child process...")
			if err := runCmd.Process.Signal(sig); err != nil {
				return err
			}

			if err := runCmd.Wait(); err != nil {
				return err
			}
		}

		return nil
	},
}

func compile(buildDir string, userConfig *bbgo.Config) error {
	if _, err := os.Stat(buildDir); os.IsNotExist(err) {
		if err := os.MkdirAll(buildDir, 0777); err != nil {
			return errors.Wrapf(err, "can not create build directory: %s", buildDir)
		}
	}

	mainFile := filepath.Join(buildDir, "main.go")
	if err := compileRunFile(mainFile, userConfig); err != nil {
		return errors.Wrap(err, "compile error")
	}

	return nil
}

func build(ctx context.Context, buildDir string, userConfig *bbgo.Config, goOS, goArch string, output *string) (string, error) {
	if err := compile(buildDir, userConfig); err != nil {
		return "", err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	buildEnvs := []string{
		"GOOS=" + goOS,
		"GOARCH=" + goArch,
	}

	buildTarget := filepath.Join(cwd, buildDir)

	binary := fmt.Sprintf("bbgow-%s-%s", goOS, goArch)
	if output != nil && len(*output) > 0 {
		binary = *output
	}

	log.Infof("building binary %s from %s...", binary, buildTarget)
	buildCmd := exec.CommandContext(ctx, "go", "build", "-tags", "wrapper", "-o", binary, buildTarget)
	buildCmd.Env = append(os.Environ(), buildEnvs...)

	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return binary, err
	}

	return binary, nil
}

func buildAndRun(ctx context.Context, userConfig *bbgo.Config, goOS, goArch string, args ...string) (*exec.Cmd, error) {
	buildDir := filepath.Join("build", "bbgow")

	binary, err := build(ctx, buildDir, userConfig, goOS, goArch, nil)
	if err != nil {
		return nil, err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	executePath := filepath.Join(cwd, binary)

	log.Infof("running wrapper binary, args: %v", args)
	runCmd := exec.Command(executePath, args...)
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	return runCmd, runCmd.Start()
}
