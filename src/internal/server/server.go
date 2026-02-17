package server

import (
	"context"
	"net/http"
	"ocf/internal/common"
	"ocf/internal/common/process"
	"ocf/internal/protocol"
	solanaclient "ocf/internal/solana"
	"ocf/internal/wallet"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
)

func StartServer() {
	if viper.GetString("wallet.account") == "" {
		common.Logger.Info("Wallet account set to 'none', skipping wallet initialization")
	} else {
		walletManager, err := wallet.InitializeWallet()
		if err != nil {
			common.Logger.Warn("Failed to initialize wallet: %v", err)
		}
		walletPublicKey := walletManager.GetPublicKey()
		common.Logger.Infof("Server wallet initialized. Public key: %s", walletPublicKey)

		if walletPublicKey == "" {
			common.Logger.Warn("No wallet public key available; ensure an account is created with `ocf wallet create`")
		}

		if viper.GetString("wallet.account") == "" {
			viper.Set("wallet.account", walletPublicKey)
		}
		if walletPath := walletManager.GetWalletPath(); walletPath != "" && viper.GetString("account.wallet") == "" {
			viper.Set("account.wallet", walletPath)
		}

		walletType := walletManager.GetWalletType()
		if walletType == wallet.WalletTypeSolana {
			common.Logger.Info("Wallet type: solana")
		} else {
			common.Logger.Info("Wallet type: ocf")
		}

		configuredAccount := viper.GetString("wallet.account")
		if configuredAccount != "" && configuredAccount != walletPublicKey {
			common.Logger.Warn("Configured wallet.account (%s) does not match local wallet public key (%s)", configuredAccount, walletPublicKey)
		}
		if configuredAccount != "" {
			common.Logger.Infof("Verified configured wallet.account matches local wallet")
		}

		owner := walletPublicKey
		if configuredAccount != "" {
			owner = configuredAccount
		}

		if walletType == wallet.WalletTypeSolana {
			mint := viper.GetString("solana.mint")
			skipVerification := viper.GetBool("solana.skip_verification")
			if mint != "" && !skipVerification {
				rpcEndpoint := viper.GetString("solana.rpc")
				client := solanaclient.NewClient(rpcEndpoint)
				verifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				hasToken, err := client.HasSPLToken(verifyCtx, owner, mint)
				cancel()
				if err != nil {
					common.Logger.Warn("Failed to verify SPL token ownership: %v", err)
				}
				if !hasToken {
					common.Logger.Warn("Solana wallet %s does not hold SPL mint %s", owner, mint)
				}
				common.Logger.Infof("Verified SPL token ownership for mint %s", mint)
			} else if mint != "" && skipVerification {
				common.Logger.Warn("Skipping Solana token ownership verification as requested")
			}
		}
	}
	owner := ""

	protocol.InitializeMyself(owner)
	_, cancelCtx := protocol.GetCRDTStore()
	defer cancelCtx()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
	defer stop()

	initTracer()
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(corsHeader())
	r.Use(gin.Recovery())
	// Initialize OpenAPI/Swagger documentation
	r.GET("/openapi.yaml", func(c *gin.Context) {
		c.Header("Content-Type", "application/yaml")
		c.File("./internal/server/openapi.yaml")
	})
	r.GET("/swagger", func(c *gin.Context) {
		c.HTML(http.StatusOK, "swagger.html", gin.H{
			"openapiUrl": "/openapi.yaml",
		})
	})
	
	// Prometheus metrics
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	go protocol.StartTicker()
	subProcess := viper.GetString("subprocess")
	if subProcess != "" {
		go process.StartCriticalProcess(subProcess)
	}
	v1 := r.Group("/v1")
	{
		v1.GET("/health", healthStatusCheck)
		crdtGroup := v1.Group("/dnt")
		{
			crdtGroup.GET("/table", getDNT)
			crdtGroup.GET("/peers", listPeers)
			crdtGroup.GET("/peers_status", listPeersWithStatus)
			crdtGroup.GET("/bootstraps", listBootstraps)
			crdtGroup.GET("/stats", getResourceStats) // Add resource manager stats endpoint
			crdtGroup.POST("/_node", updateLocal)
			crdtGroup.DELETE("/_node", deleteLocal)
		}
		p2pGroup := v1.Group("/p2p")
		{
			p2pGroup.PATCH("/:peerId/*path", P2PForwardHandler)
			p2pGroup.POST("/:peerId/*path", P2PForwardHandler)
			p2pGroup.GET("/:peerId/*path", P2PForwardHandler)
		}
		globalServiceGroup := v1.Group("/service")
		{
			globalServiceGroup.GET("/:service/*path", GlobalServiceForwardHandler)
			globalServiceGroup.POST("/:service/*path", GlobalServiceForwardHandler)
			globalServiceGroup.PATCH("/:service/*path", GlobalServiceForwardHandler)
		}
		serviceGroup := v1.Group("/_service")
		{
			serviceGroup.GET("/:service/*path", ServiceForwardHandler)
			serviceGroup.POST("/:service/*path", ServiceForwardHandler)
			serviceGroup.PATCH("/:service/*path", ServiceForwardHandler)
		}
	}
	p2plistener := P2PListener()
	srv := &http.Server{
		Addr:    "0.0.0.0:" + viper.GetString("port"),
		Handler: r,
	}
	go func() {
		err := http.Serve(p2plistener, r)
		if err != nil {
			common.Logger.Errorf("http.Serve: %s", err)
		}
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			common.ReportError(err, "Server failed to start")
		}
	}()
	go func() {
		protocol.RegisterLocalServices()
	}()
	<-ctx.Done()
	// shutting down...
	protocol.AnnounceLeave()
	protocol.ClearCRDTStore()
	time.Sleep(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	common.Logger.Info("Shutting down server gracefully")
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		common.ReportError(err, "Server shutdown failed")
	}
	common.Logger.Info("Server exiting")
}
