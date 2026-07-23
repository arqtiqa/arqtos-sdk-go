// Package arqtossdk is the public contract SDK for arqtos connectors.
//
// It defines the connector-class interfaces (starting with credential.CredentialLoader),
// the op:// secret-reference type (ref), the error taxonomy (cerr), and the skill
// schema (skillspec). Third parties build connectors against THIS module and never
// against arqtos-cli.
package arqtossdk
