// Package siwx implements the Sign-In-With-X authentication flow that
// some x402 services (Venice, Stablephone, Run402) require alongside
// or in place of per-call x402 payments.
//
// The flow is:
//
//  1. Client constructs a SIWE-style (EIP-4361) message with a fresh
//     nonce and timestamp, including the resource URL it is about to
//     call.
//  2. Client signs the message bytes with its wallet via EIP-191
//     personal_sign.
//  3. Client base64-encodes a JSON envelope containing
//     {address, message, signature, timestamp, chainId} and sets it
//     on the X-Sign-In-With-X request header.
//  4. Server verifies the signature recovers the claimed address and
//     authorizes the request against that wallet's balance/credits.
//
// SIWX is per-request (the message contains the resource URL and a
// fresh nonce), so we do not cache tokens — each call rebuilds the
// header. See the venice-x402-client SDK for the reference shape we
// match.
package siwx
