// Команда hexlet-go-crawler обходит сайт и печатает JSON-отчёт.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"code/crawler"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// execute разбирает флаги и запускает обход; Ctrl+C отменяет контекст.
func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := &cli.Command{
		Name:      "hexlet-go-crawler",
		Usage:     "analyze a website structure",
		ArgsUsage: "<url>",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "depth", Value: crawler.DefaultDepth, Usage: "crawl depth"},
			&cli.IntFlag{Name: "retries", Value: crawler.DefaultRetries, Usage: "number of retries for failed requests"},
			&cli.DurationFlag{Name: "delay", Usage: "delay between requests (example: 200ms, 1s)"},
			&cli.DurationFlag{Name: "timeout", Value: crawler.DefaultTimeout, Usage: "per-request timeout"},
			&cli.FloatFlag{Name: "rps", Usage: "limit requests per second (overrides delay)"},
			&cli.StringFlag{Name: "user-agent", Usage: "custom user agent"},
			&cli.IntFlag{Name: "workers", Value: crawler.DefaultConcurrency, Usage: "number of concurrent workers"},
		},
		Action: run,
	}

	if err := cmd.Run(ctx, os.Args); err != nil {
		return fmt.Errorf("hexlet-go-crawler: %w", err)
	}

	return nil
}

// run собирает настройки из флагов, зовёт библиотеку и печатает отчёт как есть.
func run(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return errors.New("usage: hexlet-go-crawler [options] <url> (see --help)")
	}

	report, err := crawler.Analyze(ctx, crawler.Options{
		URL:         cmd.Args().First(),
		Depth:       cmd.Int("depth"),
		Retries:     cmd.Int("retries"),
		Delay:       cmd.Duration("delay"),
		RPS:         cmd.Float("rps"),
		Timeout:     cmd.Duration("timeout"),
		UserAgent:   cmd.String("user-agent"),
		Concurrency: cmd.Int("workers"),
		IndentJSON:  true,
		HTTPClient:  &http.Client{},
	})

	if len(report) > 0 {
		if _, writeErr := os.Stdout.Write(report); writeErr != nil {
			return fmt.Errorf("write report: %w", writeErr)
		}
	}

	if err != nil {
		return fmt.Errorf("crawl: %w", err)
	}

	return nil
}
