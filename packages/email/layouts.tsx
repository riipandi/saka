import type { CSSProperties } from 'react'
import { Body, Column, Container, Head, Html, Img, Row, Section, Text } from 'react-email'

interface BaseTemplateProps {
  logoURL?: string
  appName: string
  children: React.ReactNode
}

export const BaseTemplate = ({ logoURL, appName, children }: BaseTemplateProps) => {
  const mainStyle = {
    padding: '24px',
    backgroundColor: '#FBFBFB',
    fontFamily: 'Inter, Aptos, Arial, Helvetica, sans-serif, -apple-system',
    fontSize: '16px',
    lineHeight: '1.6',
    color: '#1a1a1a'
  } satisfies CSSProperties

  const logoStyle = {
    width: '36px',
    height: '36px',
    verticalAlign: 'middle'
  } satisfies CSSProperties

  const titleStyle = {
    fontSize: '24px',
    fontWeight: 'bold',
    margin: '0',
    padding: '0'
  } satisfies CSSProperties

  const content = {
    backgroundColor: 'white',
    padding: '32px',
    borderRadius: '10px',
    boxShadow: '0 1px 4px 0px rgba(0, 0, 0, 0.1)'
  } satisfies CSSProperties

  return (
    <Html>
      <Head>
        <meta name='viewport' content='width=device-width, initial-scale=1' />
        <meta name='x-apple-disable-message-reformatting' />
      </Head>
      <Body style={mainStyle}>
        <Container style={{ maxWidth: '620px', width: '100%', margin: '0 auto' }}>
          <Section>
            <Row align='left' style={{ marginBottom: '16px' }}>
              <Column style={{ width: '50px' }}>
                <Img src={logoURL} width='36' height='36' alt={appName} style={logoStyle} />
              </Column>
              <Column>
                <Text style={titleStyle}>{appName}</Text>
              </Column>
            </Row>
          </Section>
          <div style={content}>{children}</div>
        </Container>
      </Body>
    </Html>
  )
}
