/*
 * Cadence - The resource-oriented smart contract programming language
 *
 * Copyright 2019-2020 Dapper Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */


import * as monaco from "monaco-editor";
import {editor} from "monaco-editor";
import {MonacoServices} from 'monaco-languageclient';
import {createCadenceLanguageClient} from "./language-client";
import configureCadence, {CADENCE_LANGUAGE_ID} from "./cadence";
import {CadenceLanguageServer, Callbacks} from "./language-server";
import ITextModel = editor.ITextModel;

const code1 = `
pub contract C {

    pub resource R {}

    pub fun createR(): @R {
        return <- create R()
    }
}
`

const code2 = `
import 0x1

pub fun main() {
    let r <- C.createR()

}
`

document.addEventListener('DOMContentLoaded', async () => {

  configureCadence()

  const codes = [code1, code2]

  const models: ITextModel[] = []

  for (let id = 1; id <= codes.length; id++) {
    const editorElement = document.getElementById(`editor${id}`);
    const buttonElement = document.getElementById(`button${id}`);

    const model = monaco.editor.createModel(
      codes[id - 1],
      CADENCE_LANGUAGE_ID,
      monaco.Uri.parse(`inmemory://${id}.cdc`)
    )

    models.push(model)

    const editor = monaco.editor.create(
      editorElement,
      {
        theme: 'vs-light',
        language: CADENCE_LANGUAGE_ID,
        model: model,
        minimap: {
          enabled: false
        },
      }
    );

    // The Monaco Language Client services have to be installed globally, once.
    // An editor must be passed, which is only used for commands.
    // As the Cadence language server is not providing any commands this is OK

    if (id === 1) {
      MonacoServices.install(editor);
    }

    const callbacks: Callbacks = {
      // The actual callback will be set as soon as the language server is initialized
      toServer: null,

      // The actual callback will be set as soon as the language server is initialized
      onClientClose: null,

      // The actual callback will be set as soon as the language client is initialized
      onServerClose: null,

      // The actual callback will be set as soon as the language client is initialized
      toClient: null,

      getAddressCode(address: string): string | undefined {
        if (address === '0000000000000001') {
          return models[0].getValue()
        }
      },
    }

    // The stop button demonstrates how to dispose the editor
    // and stop the language server

    buttonElement.addEventListener('click', () => {
      editor.dispose()
      callbacks.onClientClose()
    })

    // Start one language server per editor.
    // Even though one language server can handle multiple documents,
    // this demonstrates this is possible and is more resilient:
    // if the server for one editor crashes, it does not break the other editors

    await CadenceLanguageServer.create(callbacks);

    const languageClient = createCadenceLanguageClient(callbacks);
    languageClient.start()
  }
})
