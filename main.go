/*
Copyright © 2022 buchhalter.ai <support@buchhalter.ai>
*/
package main

import (
	"buchhalter/cmd"
	"github.com/joho/godotenv"
	"io"
	"log"
)

func main() {
	log.SetOutput(io.Discard)
	godotenv.Load()
	cmd.Execute()
}
