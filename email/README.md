# inkan email

A key mail of your own, designed with [React Email](https://react.email) and
built into the worker.

```sh
pnpm install
pnpm dev
```

`pnpm dev` previews `emails/license.tsx` in the browser with a made up key.
Change the component until the mail is yours.

```sh
pnpm build
```

This renders the mail with placeholders where the key and the customer go and
writes it to `../worker/src/template.ts`. Deploy the worker and it sends this
mail. Commit that file in your fork, the repository never changes it.

To get the built-in mail back, set the template to an empty string again.

## What the worker fills in

| Prop         | Placeholder   |                                    |
| ------------ | ------------- | ---------------------------------- |
| `licenseKey` | `{{key}}`     | The key                            |
| `name`       | `{{name}}`    | `INKAN_PRODUCT_NAME` or the prefix |
| `plan`       | `{{plan}}`    | The plan of the license            |
| `email`      | `{{email}}`   | Who the license is for             |
| `expires`    | `{{expires}}` | 16 October 2096, or Never          |

The props are plain text, so show them and do not branch on them. During the
build they hold the placeholder, not a value.

The `<title>` is the subject of the mail. The plain text part stays the one of
the worker.
