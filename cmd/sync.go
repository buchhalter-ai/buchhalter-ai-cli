package cmd

import (
	"buchhalter/lib/archive"
	"buchhalter/lib/browser"
	"buchhalter/lib/client"
	"buchhalter/lib/repository"
	"buchhalter/lib/utils"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/viper"

	"buchhalter/lib/parser"
	"buchhalter/lib/vault"

	"github.com/spf13/cobra"
)

const (
	padding  = 2
	maxWidth = 80
)

var (
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6D58E")).Margin(1, 0)
	dotStyle      = helpStyle.Copy().UnsetMargins()
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EA4335"))
	durationStyle = dotStyle.Copy()
	spinnerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#D6D58E"))
	appStyle      = lipgloss.NewStyle().Margin(1, 2, 0, 2)
	vaultItems    []vault.Item
	args          []string
	choices       = []string{"Yes", "No", "Always yes (don't ask again)"}
	ChromeVersion string
	RunData       repository.RunData
)

type tickMsg time.Time
type model struct {
	mode          string
	currentAction string
	details       string
	showProgress  bool
	progress      progress.Model
	spinner       spinner.Model
	results       []resultMsg
	quitting      bool
	hasError      bool
	cursor        int
	choice        string
}

type quitMsg struct{}

type resultMsg struct {
	duration      time.Duration
	step          string
	errorMessage  string
	newFilesCount int
}

type resultModeUpdate struct {
	mode    string
	title   string
	details string
}

type resultStatusUpdate struct {
	title    string
	hasError bool
}

type ResultProgressUpdate struct {
	Percent float64
}

type recipeToExecute struct {
	recipe      *parser.Recipe
	vaultItemId string
}

func (r resultMsg) String() string {
	s := len(r.step)
	if r.duration == 0 {
		if r.step != "" {
			r.step = r.step + " " + strings.Repeat(".", maxWidth-1-s)
			return r.step
		}
		return dotStyle.Render(strings.Repeat(".", maxWidth))
	}
	d := r.duration.Round(time.Second).String()
	fill := strings.Repeat(".", maxWidth-1-s-(len(d)-8))
	return fmt.Sprintf("%s %s%s", r.step, fill, durationStyle.Render(d))
}

func (m model) Init() tea.Cmd {
	return tea.Sequence(
		m.spinner.Tick,
	)
}

func initialModel() model {
	const numLastResults = 5

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = spinnerStyle

	m := model{
		mode:          "sync",
		currentAction: "Initializing...",
		details:       "Loading...",
		showProgress:  true,
		progress:      progress.New(progress.WithGradient("#9FC131", "#DBF227")),
		spinner:       s,
		results:       make([]resultMsg, numLastResults),
		hasError:      false,
	}
	return m
}

func init() {
	rootCmd.AddCommand(syncCmd)
}

func quit(m model) model {
	if m.hasError {
		m.currentAction = "ERROR while running recipes!"
		m.quitting = true
		m.showProgress = false
	} else {
		m.currentAction = "Thanks for using buchhalter.ai!"
		m.quitting = true
		m.showProgress = false
		m.details = "HAVE A NICE DAY! :)"
	}
	go browser.Quit()
	go client.Quit()
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			mn := quit(m)
			return mn, tea.Quit
		case "enter":
			// Send the choice on the channel and exit.
			m.choice = choices[m.cursor]
			m.mode = "sync"
			switch m.choice {
			case "Yes":
				sendMetrics(false)
				mn := quit(m)
				return mn, tea.Quit
			case "No":
				mn := quit(m)
				return mn, tea.Quit
			case "Always yes (don't ask again)":
				sendMetrics(true)
				mn := quit(m)
				return mn, tea.Quit
			}
		case "down", "j":
			m.cursor++
			if m.cursor >= len(choices) {
				m.cursor = 0
			}
		case "up", "k":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(choices) - 1
			}
		}
		return m, nil
	case quitMsg:
		mn := quit(m)
		return mn, tea.Quit
	case tea.WindowSizeMsg:
		m.progress.Width = msg.Width - padding*2 - 4
		if m.progress.Width > maxWidth {
			m.progress.Width = maxWidth
		}
		return m, nil
	case resultMsg:
		m.results = append(m.results[1:], msg)
		if msg.errorMessage != "" {
			m.hasError = true
			m.details = msg.errorMessage
			mn := quit(m)
			return mn, tea.Quit
		}
		return m, nil
	case resultStatusUpdate:
		m.currentAction = msg.title
		if msg.hasError {
			m.hasError = true
		}
		return m, nil
	case resultModeUpdate:
		m.currentAction = msg.title
		m.details = msg.details
		m.mode = msg.mode
		m.showProgress = false
		return m, nil
	case ResultProgressUpdate:
		cmd := m.progress.SetPercent(msg.Percent)
		return m, cmd
	case utils.ResultProgressUpdate:
		cmd := m.progress.SetPercent(msg.Percent)
		return m, cmd
	case utils.ResultTitleAndDescriptionUpdate:
		m.currentAction = msg.Title
		m.details = msg.Description
		return m, nil
	case tickMsg:
		if m.progress.Percent() == 1.0 {
			m.showProgress = false
			return m, nil
		}
		cmd := m.progress.IncrPercent(0.25)
		return m, tea.Batch(tickCmd(), cmd)

	// FrameMsg is sent when the progress bar wants to animate itself
	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd

	default:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*1, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) View() string {
	var s string
	s = fmt.Sprintf(
		"%s\n%s\n%s%s\n%s\n",
		headerStyle(LogoText),
		textStyle("Automatically sync all your incoming invoices from your suppliers. "),
		textStyle("More information at: "),
		textStyleBold("https://buchhalter.ai"),
		textStyleGrayBold("Using OICDB "+parser.OicdbVersion+" and CLI "+CliVersion),
	) + "\n"

	if !m.hasError {
		s += m.spinner.View() + m.currentAction
		s += helpStyle.Render("  " + m.details)
	} else {
		s += errorStyle.Render("ERROR: " + m.currentAction)
		s += helpStyle.Render("  " + m.details)
	}

	s += "\n"

	if m.showProgress {
		s += m.progress.View() + "\n\n"
	}

	if !m.hasError && m.mode == "sync" {
		for _, res := range m.results {
			s += res.String() + "\n"
		}
	}

	if m.mode == "sendMetrics" && !m.quitting {
		for i := 0; i < len(choices); i++ {
			if m.cursor == i {
				s += "(•) "
			} else {
				s += "( ) "
			}
			s += choices[i]
			s += "\n"
		}
	}

	if !m.quitting {
		s += helpStyle.Render("Press q to exit")
	}

	if m.quitting {
		s += "\n"
	}

	return appStyle.Render(s)
}

func sendMetrics(a bool) {
	repository.SendMetrics(RunData, CliVersion, ChromeVersion)
	if a {
		viper.Set("buchhalter_always_send_metrics", true)
		_ = viper.WriteConfig()
	}
}

func runRecipes(p *tea.Program, provider string, vaultItems []vault.Item) {
	t := "Build archive index"
	p.Send(resultStatusUpdate{title: t})
	archive.BuildArchiveIndex()

	if !viper.GetBool("dev") {
		t = "Checking for repository updates"
		p.Send(resultStatusUpdate{title: t})
		err := repository.UpdateIfAvailable()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	r := prepareRecipes(provider, vaultItems)

	rc := len(r)
	t = "Running recipes for " + fmt.Sprintf("%d", rc) + " suppliers..."
	if rc == 1 {
		t = "Running one recipe..."
	}
	p.Send(resultStatusUpdate{title: t})
	p.Send(ResultProgressUpdate{Percent: 0.001})

	tsc := 0 //total steps count
	scs := 0 //count steps current recipe
	bcs := 0 //base count steps
	var recipeResult utils.RecipeResult
	for i := range r {
		tsc += len(r[i].recipe.Steps)
	}
	for i := range r {
		s := time.Now()
		scs = len(r[i].recipe.Steps)
		p.Send(resultStatusUpdate{title: "Downloading invoices from " + r[i].recipe.Provider + ":", hasError: false})
		switch r[i].recipe.Type {
		case "browser":
			recipeResult = browser.RunRecipe(p, tsc, scs, bcs, r[i].recipe, r[i].vaultItemId)
			if ChromeVersion == "" {
				ChromeVersion = browser.ChromeVersion
			}
		case "client":
			recipeResult = client.RunRecipe(p, tsc, scs, bcs, r[i].recipe, r[i].vaultItemId)
			if ChromeVersion == "" {
				ChromeVersion = client.ChromeVersion
			}
		}
		rdx := repository.RunDataProvider{
			Provider:         r[i].recipe.Provider,
			Version:          r[i].recipe.Version,
			Status:           recipeResult.StatusText,
			LastErrorMessage: recipeResult.LastErrorMessage,
			Duration:         time.Since(s).Seconds(),
			NewFilesCount:    recipeResult.NewFilesCount,
		}
		RunData = append(RunData, rdx)
		p.Send(resultMsg{duration: time.Since(s), newFilesCount: recipeResult.NewFilesCount, step: recipeResult.StatusTextFormatted, errorMessage: recipeResult.LastErrorMessage})
		bcs += scs
	}

	if viper.GetBool("buchhalter_always_send_metrics") {
		repository.SendMetrics(RunData, CliVersion, ChromeVersion)
		p.Send(quitMsg{})
	} else if viper.GetBool("dev") {
		p.Send(quitMsg{})
	} else {
		p.Send(resultModeUpdate{
			mode:    "sendMetrics",
			title:   "Let's improve buchhalter-cli together!",
			details: "Allow buchhalter-cli to send anonymized usage data to our api?",
		})
	}
}

func prepareRecipes(provider string, vaultItems []vault.Item) []recipeToExecute {
	parser.LoadRecipes()

	// Run single provider recipe
	var r []recipeToExecute
	sc := 0
	if provider != "" {
		for i := range vaultItems {
			// Check if a recipe exists for the item
			recipe := parser.GetRecipeForItem(vaultItems[i])
			if recipe != nil && provider == recipe.Provider {
				r = append(r, recipeToExecute{recipe, vaultItems[i].ID})
			}
		}
	} else {
		// Run all recipes
		for i := range vaultItems {
			// Check if a recipe exists for the item
			recipe := parser.GetRecipeForItem(vaultItems[i])
			if recipe != nil {
				sc = sc + len(recipe.Steps)
				r = append(r, recipeToExecute{recipe, vaultItems[i].ID})
			}
		}
	}
	return r
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Synchronize all invoices from your suppliers",
	Long:  "The sync command uses all buchhalter tagged credentials from your vault and synchronizes all invoices.",
	Run: func(cmd *cobra.Command, cmdArgs []string) {
		args = cmdArgs
		provider := ""
		if len(args) > 0 {
			provider = args[0]
		}
		p := tea.NewProgram(initialModel())

		// Load vault items/try to connect to 1password cli
		var errorMessage string
		vaultItems, errorMessage = vault.LoadVaultItems()
		if errorMessage != "" {
			fmt.Println(errorMessage)
			os.Exit(0)
		}

		// Run recipes
		go runRecipes(p, provider, vaultItems)

		if _, err := p.Run(); err != nil {
			fmt.Println("Error running program:", err)
			os.Exit(1)
		}
	},
}
