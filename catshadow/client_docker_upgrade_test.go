// SPDX-FileCopyrightText: 2024 Katzenpost developers
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// client.go - client
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

//go:build docker_test
// +build docker_test

package catshadow

import (
	"context"
	"testing"
	"time"
	"os"

	_ "net/http/pprof"

	"github.com/katzenpost/hpqc/rand"
	"github.com/stretchr/testify/require"
)

func TestUpgradeResume(t *testing.T) {
	t.Parallel()
	require := require.New(t)

	// if testdata/alice_state exists, load it, otherwise, create it
	// if testdata/bob_state exists, load it, otherwise, create it
	var alice, bob *Client
	aliceStateFilePath := createRandomStateFile(t)
	bobStateFilePath := createRandomStateFile(t)
	_, aErr := os.Open("testdata/alice_state")
	_, bErr := os.Open("testdata/bob_state")

	if aErr == nil && bErr == nil {
		// copy testdata state into the temporary statefile location
		// because the client will mutate the statefile when started
		err := copyFile("testdata/alice_state", aliceStateFilePath)
		require.NoError(err)
		err = copyFile("testdata/bob_state", bobStateFilePath)
		require.NoError(err)
	} else {
		// create 2 statefiles for a pair of contacts
		alice = createCatshadowClientWithState(t, aliceStateFilePath)
		bob = createCatshadowClientWithState(t, bobStateFilePath)

		sharedSecret := []byte("wait for key exchange")
		randBytes := [8]byte{}
		_, err := rand.Reader.Read(randBytes[:])
		require.NoError(err)
		sharedSecret = append(sharedSecret, randBytes[:]...)

		alice.NewContact("bob", sharedSecret)
		bob.NewContact("alice", sharedSecret)

		ctx, cancelFn := context.WithTimeout(context.Background(), time.Minute)
		evt := waitForEvent(ctx,  alice.EventSink, &KeyExchangeCompletedEvent{})
		cancelFn()
		ev, ok := evt.(*KeyExchangeCompletedEvent)
		require.True(ok)
		require.NoError(ev.Err)

		ctx, cancelFn = context.WithTimeout(context.Background(), time.Minute)
		evt = waitForEvent(ctx, bob.EventSink, &KeyExchangeCompletedEvent{})
		cancelFn()
		ev, ok = evt.(*KeyExchangeCompletedEvent)
		require.True(ok)
		require.NoError(ev.Err)

		// alice halts her client
		alice.Shutdown()

		// bob sends a message
		bob.SendMessage("alice", []byte("blah"))
		ctx, cancelFn = context.WithTimeout(context.Background(), time.Minute)
		evt = waitForEvent(ctx, bob.EventSink, &MessageDeliveredEvent{})
		cancelFn()
		_, ok = evt.(*MessageDeliveredEvent)
		require.True(ok)

		// bob halts his client
		bob.Shutdown()

		// save the statefiles into testdata for using with later versions of catshadow
		err = copyFile(aliceStateFilePath, "testdata/alice_state")
		require.NoError(err)
		err = copyFile(bobStateFilePath, "testdata/bob_state")
		require.NoError(err)
	}

	// start alice
	alice = reloadCatshadowState(t, "testdata/alice_state")

	// receive bob's message
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Minute)
	evt := waitForEvent(ctx,  alice.EventSink, &MessageReceivedEvent{})
	cancelFn()
	switch ev := evt.(type) {
	case *MessageReceivedEvent:
		require.Equal(ev.Nickname, "bob")
	default:
		t.Fail()
	}

	// alice writes to bob
	alice.SendMessage("bob", []byte("blah"))

	// start bob
	bob = reloadCatshadowState(t, "testdata/bob_state")

	// bob receives alice's message
	ctx, cancelFn = context.WithTimeout(context.Background(), time.Minute)
	evt = waitForEvent(ctx,  bob.EventSink, &MessageReceivedEvent{})
	cancelFn()
	switch ev := evt.(type) {
	case *MessageReceivedEvent:
		require.Equal(ev.Nickname, "alice")
	default:
		t.Fail()
	}
}
